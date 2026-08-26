package service

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	agentv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/agent/v1"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
)

const (
	maxMissionSourceBytes = 1 << 20
	maxMissionLineBytes   = 4096
	maxMissionItems       = 200
	maxIdempotencyKeyLen  = 128
)

var supportedMissionCommands = map[int]string{
	16: "NAV_WAYPOINT",
	21: "NAV_LAND",
	22: "NAV_TAKEOFF",
}

var supportedMissionFrames = map[int]string{
	0: "GLOBAL",
}

// ImportMissionRequest carries a bounded WPL source plus stale-screen binding
// preconditions. The server derives the authoritative binding from the flight.
type ImportMissionRequest struct {
	SourceFormat  domain.MissionSourceFormat `json:"source_format"`
	Source        string                     `json:"source"`
	AircraftID    string                     `json:"aircraft_id"`
	IntentID      string                     `json:"intent_id"`
	IntentVersion int                        `json:"intent_version"`
}

// ImportMissionResult reports whether an idempotency-key retry replayed the original result.
type ImportMissionResult struct {
	Mission  domain.Mission `json:"mission"`
	Replayed bool           `json:"replayed"`
}

// MissionValidationError contains machine-readable WPL validation findings.
type MissionValidationError struct {
	Findings []domain.MissionValidationFinding
}

// Error returns a stable validation summary suitable for logs and HTTP errors.
//
// Returns:
//   - result: describes how many mission validation findings blocked import.
func (e MissionValidationError) Error() string {
	return fmt.Sprintf("%s: mission validation produced %d blocking finding(s)", ErrValidation, len(e.Findings))
}

// Unwrap exposes ErrValidation for errors.Is classification.
//
// Returns:
//   - error: is ErrValidation.
func (e MissionValidationError) Unwrap() error { return ErrValidation }

// ImportMission validates and immutably stores one mission version bound to a
// flight's server-owned aircraft and exact operational-intent version. It never
// creates or modifies operational volumes.
//
// Parameters:
//   - ctx: controls cancellation and durable persistence.
//   - flightID: identifies the planned flight receiving the mission.
//   - idempotencyKey: globally identifies this import request and is required.
//   - req: carries WPL source and client-observed binding preconditions.
//
// Returns:
//   - result: contains the stored mission and whether this was an exact replay.
//   - error: reports malformed source, stale binding, lifecycle, idempotency, or persistence failures.
func (s *FleetService) ImportMission(ctx context.Context, flightID string, idempotencyKey string, req ImportMissionRequest) (ImportMissionResult, error) {
	flightID = strings.TrimSpace(flightID)
	idempotencyKey = strings.TrimSpace(idempotencyKey)
	req.AircraftID = strings.TrimSpace(req.AircraftID)
	req.IntentID = strings.TrimSpace(req.IntentID)
	if flightID == "" || req.AircraftID == "" || req.IntentID == "" || req.IntentVersion <= 0 {
		return ImportMissionResult{}, fmt.Errorf("%w: flight_id, aircraft_id, intent_id, and positive intent_version are required", ErrValidation)
	}
	if err := validateIdempotencyKey(idempotencyKey); err != nil {
		return ImportMissionResult{}, err
	}
	if req.SourceFormat != domain.MissionSourceFormatQGCWPL110 {
		return ImportMissionResult{}, fmt.Errorf("%w: source_format must be %q", ErrValidation, domain.MissionSourceFormatQGCWPL110)
	}
	sourceSHA := sha256Hex(req.Source)
	requestHash := sha256Hex(strings.Join([]string{
		flightID, req.AircraftID, req.IntentID, strconv.Itoa(req.IntentVersion), string(req.SourceFormat), sourceSHA,
	}, "\n"))
	existing, err := s.durable.GetMissionByIdempotencyKey(ctx, idempotencyKey)
	if err == nil {
		if existing.IdempotencyRequest != requestHash {
			return ImportMissionResult{}, durable.ErrIdempotencyConflict
		}
		return ImportMissionResult{Mission: existing, Replayed: true}, nil
	}
	if !errors.Is(err, durable.ErrNotFound) {
		return ImportMissionResult{}, fmt.Errorf("get mission idempotency record: %w", err)
	}

	items, findings, canonicalSHA, parsedSourceSHA, err := parseWPL110(req.Source)
	if err != nil {
		return ImportMissionResult{}, err
	}
	if parsedSourceSHA != sourceSHA {
		return ImportMissionResult{}, errors.New("mission source hash changed during import")
	}
	flight, err := s.durable.GetFlightRecord(ctx, flightID)
	if err != nil {
		return ImportMissionResult{}, fmt.Errorf("get flight record: %w", err)
	}
	if flight.Status != domain.FlightStatusPlanned {
		return ImportMissionResult{}, fmt.Errorf("%w: missions may only be imported while flight %s is planned", ErrInvalidTransition, flight.ID)
	}
	if flight.AircraftID != req.AircraftID || flight.IntentID != req.IntentID || flight.IntentVersion != req.IntentVersion {
		return ImportMissionResult{}, fmt.Errorf("%w: requested mission binding does not match the flight's aircraft and exact intent version", ErrValidation)
	}
	intent, err := s.durable.GetOperationalIntentVersion(ctx, flight.IntentID, flight.IntentVersion)
	if err != nil {
		return ImportMissionResult{}, fmt.Errorf("get linked operational intent version: %w", err)
	}
	currentIntent, err := s.durable.GetOperationalIntent(ctx, flight.IntentID)
	if err != nil {
		return ImportMissionResult{}, fmt.Errorf("get current operational intent: %w", err)
	}
	if currentIntent.Version != flight.IntentVersion {
		return ImportMissionResult{}, fmt.Errorf("%w: flight is bound to superseded intent version %d", ErrInvalidTransition, flight.IntentVersion)
	}
	if intent.AircraftID != flight.AircraftID || currentIntent.AircraftID != flight.AircraftID {
		return ImportMissionResult{}, fmt.Errorf("%w: flight and operational-intent aircraft bindings disagree", ErrValidation)
	}
	if intent.Status != domain.IntentStatusAccepted && intent.Status != domain.IntentStatusActive {
		return ImportMissionResult{}, fmt.Errorf("%w: mission import requires an accepted or active intent", ErrInvalidTransition)
	}
	aircraft, err := s.durable.GetAircraft(ctx, flight.AircraftID)
	if err != nil {
		return ImportMissionResult{}, fmt.Errorf("get bound aircraft: %w", err)
	}
	operatorID, err := consistentOperatorID(flight.OperatorID, intent.OperatorID, aircraft.OperatorID)
	if err != nil {
		return ImportMissionResult{}, err
	}
	if operatorID == "" {
		return ImportMissionResult{}, fmt.Errorf("%w: bound operator_id is required for mission deployment", ErrValidation)
	}
	if err := s.validateMissionAgainstIntent(ctx, intent, items); err != nil {
		return ImportMissionResult{}, err
	}

	mission := domain.Mission{
		ID: uuid.NewString(), OperatorID: operatorID, FlightID: flight.ID, AircraftID: flight.AircraftID,
		IntentID: flight.IntentID, IntentVersion: flight.IntentVersion,
		SourceFormat: req.SourceFormat, SourceSHA256: sourceSHA, MissionDigest: canonicalSHA,
		IdempotencyKey: idempotencyKey, IdempotencyRequest: requestHash,
		ValidationFindings: findings, Items: items, CreatedAt: s.now().UTC(),
	}
	stored, err := s.durable.CreateMission(ctx, mission)
	if err != nil {
		return ImportMissionResult{}, fmt.Errorf("create mission: %w", err)
	}
	return ImportMissionResult{Mission: stored, Replayed: stored.ID != mission.ID}, nil
}

func (s *FleetService) validateMissionAgainstIntent(ctx context.Context, intent domain.OperationalIntent, items []domain.MissionItem) error {
	allVolumes, err := s.durable.ListOperationalVolumes(ctx, intent.ID)
	if err != nil {
		return fmt.Errorf("list linked operational volumes: %w", err)
	}
	volumes := volumesForVersion(allVolumes, intent.Version)
	if len(volumes) != 1 {
		return MissionValidationError{Findings: []domain.MissionValidationFinding{missionFinding(
			"unsupported_volume_topology",
			fmt.Sprintf("first mission slice requires exactly one volume for intent version %d; found %d", intent.Version, len(volumes)), nil,
		)}}
	}
	volume := volumes[0]
	if volume.AltitudeRef != domain.AltitudeReferenceMSL {
		return MissionValidationError{Findings: []domain.MissionValidationFinding{missionFinding(
			"unsupported_altitude_reference", "first mission slice requires a GLOBAL WPL mission and MSL operational volume", nil,
		)}}
	}
	if volume.StartsAt.After(intent.PlannedStartAt) || volume.EndsAt.Before(intent.PlannedEndAt) {
		return MissionValidationError{Findings: []domain.MissionValidationFinding{missionFinding(
			"ambiguous_volume_time", "the single operational volume must cover the entire intent window because WPL 110 has no waypoint schedule", nil,
		)}}
	}
	findings := make([]domain.MissionValidationFinding, 0)
	for _, item := range items {
		if item.AltitudeM < volume.MinAltitudeM || item.AltitudeM > volume.MaxAltitudeM {
			sequence := item.Sequence
			findings = append(findings, missionFinding(
				"mission_altitude_outside_authorized_volume",
				fmt.Sprintf("item %d altitude %.3f m MSL is outside authorized range %.3f to %.3f m MSL", item.Sequence, item.AltitudeM, volume.MinAltitudeM, volume.MaxAltitudeM),
				&sequence,
			))
		}
	}
	coverage, err := s.durable.CheckMissionCoverage(ctx, volume, items)
	if err != nil {
		return MissionValidationError{Findings: []domain.MissionValidationFinding{missionFinding(
			"authorized_geometry_not_evaluable", fmt.Sprintf("authorized volume geometry cannot be evaluated: %v", err), nil,
		)}}
	}
	for _, sequence := range coverage.UncoveredItems {
		itemSequence := sequence
		findings = append(findings, missionFinding(
			"mission_item_outside_authorized_volume", fmt.Sprintf("item %d is outside the authorized footprint", sequence), &itemSequence,
		))
	}
	for _, sequence := range coverage.UncoveredSegments {
		segmentSequence := sequence
		findings = append(findings, missionFinding(
			"mission_segment_outside_authorized_volume", fmt.Sprintf("route segment %d to %d leaves the authorized footprint", sequence, sequence+1), &segmentSequence,
		))
	}
	if len(findings) > 0 {
		return MissionValidationError{Findings: findings}
	}
	return nil
}

// GetCurrentMission returns the latest immutable mission version for a flight
// after revalidating that its stored binding still matches the flight record.
//
// Parameters:
//   - ctx: controls cancellation and durable reads.
//   - flightID: identifies the flight whose current mission is requested.
//
// Returns:
//   - mission: is the latest stored version.
//   - error: reports missing data, inconsistent binding, or persistence failures.
func (s *FleetService) GetCurrentMission(ctx context.Context, flightID string) (domain.Mission, error) {
	flightID = strings.TrimSpace(flightID)
	if flightID == "" {
		return domain.Mission{}, fmt.Errorf("%w: flight_id is required", ErrValidation)
	}
	flight, err := s.durable.GetFlightRecord(ctx, flightID)
	if err != nil {
		return domain.Mission{}, fmt.Errorf("get flight record: %w", err)
	}
	mission, err := s.durable.GetCurrentMissionForFlight(ctx, flightID)
	if err != nil {
		return domain.Mission{}, fmt.Errorf("get current mission: %w", err)
	}
	if mission.AircraftID != flight.AircraftID || mission.IntentID != flight.IntentID || mission.IntentVersion != flight.IntentVersion {
		return domain.Mission{}, fmt.Errorf("mission binding is inconsistent with flight: %w", durable.ErrVersionConflict)
	}
	if mission.OperatorID != flight.OperatorID {
		return domain.Mission{}, fmt.Errorf("mission operator binding is inconsistent with flight: %w", durable.ErrVersionConflict)
	}
	return mission, nil
}

func validateIdempotencyKey(key string) error {
	if key == "" || len(key) > maxIdempotencyKeyLen {
		return fmt.Errorf("%w: Idempotency-Key is required and must be at most %d bytes", ErrValidation, maxIdempotencyKeyLen)
	}
	for _, r := range key {
		if r < 0x21 || r > 0x7e {
			return fmt.Errorf("%w: Idempotency-Key must contain visible ASCII characters only", ErrValidation)
		}
	}
	return nil
}

func parseWPL110(source string) ([]domain.MissionItem, []domain.MissionValidationFinding, string, string, error) {
	if len(source) == 0 || len(source) > maxMissionSourceBytes {
		return nil, nil, "", "", MissionValidationError{Findings: []domain.MissionValidationFinding{{
			Severity: "error", Code: "source_size", Message: fmt.Sprintf("source must contain 1 to %d bytes", maxMissionSourceBytes),
		}}}
	}
	sourceSHA := sha256Hex(source)
	scanner := bufio.NewScanner(strings.NewReader(source))
	scanner.Buffer(make([]byte, 1024), maxMissionLineBytes)
	lineNumber := 0
	items := make([]domain.MissionItem, 0)
	findings := make([]domain.MissionValidationFinding, 0)
	hasTakeoff := false
	hasLanding := false
	sourceItemCount := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSuffix(scanner.Text(), "\r")
		if lineNumber == 1 {
			if line != "QGC WPL 110" {
				findings = append(findings, missionFinding("invalid_header", "first line must be exactly QGC WPL 110", nil))
			}
			continue
		}
		if strings.TrimSpace(line) == "" {
			continue
		}
		if sourceItemCount > maxMissionItems {
			findings = append(findings, missionFinding("too_many_items", fmt.Sprintf("mission may contain at most %d items", maxMissionItems), nil))
			break
		}
		fields := strings.Split(line, "\t")
		if len(fields) != 12 {
			seq := sourceItemCount
			findings = append(findings, missionFinding("invalid_field_count", fmt.Sprintf("line %d must contain 12 tab-separated fields", lineNumber), &seq))
			sourceItemCount++
			continue
		}
		item, itemFindings := parseWPLItem(fields, sourceItemCount, lineNumber)
		if sourceItemCount == 0 && (!item.Current || item.Frame != 0 || item.Command != 16) {
			sequence := 0
			itemFindings = append(itemFindings, missionFinding(
				"invalid_home_metadata", "WPL row 0 must be HOME metadata with sequence 0, current 1, frame 0, and command 16", &sequence,
			))
		}
		if len(itemFindings) == 0 && sourceItemCount > 0 && item.Current {
			sequence := sourceItemCount - 1
			itemFindings = append(itemFindings, missionFinding(
				"operational_item_marked_current", fmt.Sprintf("WPL source row %d must set current to 0", sourceItemCount), &sequence,
			))
		}
		if sourceItemCount > 0 {
			sequence := sourceItemCount - 1
			if !item.Autocontinue {
				itemFindings = append(itemFindings, missionFinding(
					"autocontinue_required", fmt.Sprintf("WPL source row %d must set autocontinue to 1", sourceItemCount), &sequence,
				))
			}
			if item.Command == 21 {
				if item.Param1 != 0 || item.Param2 != 0 || item.Param3 != 0 || (item.Param4 != 0 && item.Param4 != 1) {
					itemFindings = append(itemFindings, missionFinding(
						"nonzero_parameters_unsupported", fmt.Sprintf("WPL LAND source row %d requires params 1 through 3 zero and param 4 zero or one", sourceItemCount), &sequence,
					))
				} else {
					// ArduPilot normalizes NAV_LAND param4=0 to +1 on storage and readback.
					// Canonicalize both accepted source spellings before hashing so readback
					// compares against the actual onboard representation.
					item.Param4 = 1
				}
			} else if item.Param1 != 0 || item.Param2 != 0 || item.Param3 != 0 || item.Param4 != 0 {
				itemFindings = append(itemFindings, missionFinding(
					"nonzero_parameters_unsupported", fmt.Sprintf("WPL source row %d params 1 through 4 must be zero in the first ArduPilot slice", sourceItemCount), &sequence,
				))
			}
			if !missionAltitudeCMRoundTrips(item.AltitudeM) {
				itemFindings = append(itemFindings, missionFinding(
					"altitude_not_centimeter_roundtrippable", fmt.Sprintf("WPL source row %d altitude must round-trip through ArduPilot centimeter storage", sourceItemCount), &sequence,
				))
			}
		}
		findings = append(findings, itemFindings...)
		if len(itemFindings) == 0 && sourceItemCount > 0 {
			item.Sequence = sourceItemCount - 1
			items = append(items, item)
			hasTakeoff = hasTakeoff || item.Command == 22
			hasLanding = hasLanding || item.Command == 21
		}
		sourceItemCount++
	}
	if err := scanner.Err(); err != nil {
		findings = append(findings, missionFinding("line_too_long", fmt.Sprintf("mission line exceeds %d bytes", maxMissionLineBytes), nil))
	}
	if lineNumber == 0 {
		findings = append(findings, missionFinding("invalid_header", "first line must be exactly QGC WPL 110", nil))
	}
	if sourceItemCount == 0 {
		findings = append(findings, missionFinding("missing_home_metadata", "mission must contain WPL row 0 HOME metadata", nil))
	}
	if len(items) == 0 {
		findings = append(findings, missionFinding("empty_mission", "mission must contain at least one operational item after HOME metadata", nil))
	}
	if len(findings) > 0 {
		return nil, nil, "", sourceSHA, MissionValidationError{Findings: findings}
	}
	warnings := make([]domain.MissionValidationFinding, 0, 2)
	if !hasTakeoff {
		warnings = append(warnings, missionWarning("takeoff_not_declared", "mission contains no NAV_TAKEOFF item"))
	}
	if !hasLanding {
		warnings = append(warnings, missionWarning("landing_not_declared", "mission contains no NAV_LAND item"))
	}
	digest, err := canonicalMissionSHA(items)
	if err != nil {
		return nil, nil, "", sourceSHA, fmt.Errorf("encode canonical mission plan: %w", err)
	}
	return items, warnings, digest, sourceSHA, nil
}

func parseWPLItem(fields []string, expectedSequence int, lineNumber int) (domain.MissionItem, []domain.MissionValidationFinding) {
	seq := expectedSequence
	findings := make([]domain.MissionValidationFinding, 0)
	ints := make([]int, 4)
	for i, index := range []int{0, 1, 2, 3} {
		value, err := strconv.Atoi(fields[index])
		if err != nil {
			findings = append(findings, missionFinding("invalid_integer", fmt.Sprintf("line %d field %d is not an integer", lineNumber, index+1), &seq))
			continue
		}
		ints[i] = value
	}
	values := make([]float64, 7)
	for i, index := range []int{4, 5, 6, 7, 8, 9, 10} {
		value, err := strconv.ParseFloat(fields[index], 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			findings = append(findings, missionFinding("invalid_number", fmt.Sprintf("line %d field %d must be finite", lineNumber, index+1), &seq))
			continue
		}
		values[i] = value
	}
	for _, index := range []int{0, 1, 2, 3} {
		canonical := float32(values[index])
		if math.IsInf(float64(canonical), 0) || math.IsNaN(float64(canonical)) {
			findings = append(findings, missionFinding("number_out_of_float32_range", fmt.Sprintf("line %d field %d cannot be represented by MAVLink float32", lineNumber, index+5), &seq))
			continue
		}
		if values[index] == 0 {
			values[index] = 0
		}
	}
	canonicalAltitude := float32(values[6])
	if math.IsInf(float64(canonicalAltitude), 0) || math.IsNaN(float64(canonicalAltitude)) {
		findings = append(findings, missionFinding("number_out_of_float32_range", fmt.Sprintf("line %d field 11 cannot be represented by MAVLink float32", lineNumber), &seq))
	} else if canonicalAltitude == 0 {
		values[6] = 0
	} else {
		values[6] = float64(canonicalAltitude)
	}
	autoContinue, err := strconv.Atoi(fields[11])
	if err != nil || (autoContinue != 0 && autoContinue != 1) {
		findings = append(findings, missionFinding("invalid_auto_continue", fmt.Sprintf("line %d auto_continue must be 0 or 1", lineNumber), &seq))
	}
	if ints[0] != expectedSequence {
		findings = append(findings, missionFinding("non_contiguous_sequence", fmt.Sprintf("line %d sequence must be %d", lineNumber, expectedSequence), &seq))
	}
	if ints[1] != 0 && ints[1] != 1 {
		findings = append(findings, missionFinding("invalid_current", fmt.Sprintf("line %d current must be 0 or 1", lineNumber), &seq))
	}
	if _, ok := supportedMissionFrames[ints[2]]; !ok {
		findings = append(findings, missionFinding("unsupported_frame", fmt.Sprintf("line %d frame %d is not supported", lineNumber, ints[2]), &seq))
	}
	if _, ok := supportedMissionCommands[ints[3]]; !ok {
		findings = append(findings, missionFinding("unsupported_command", fmt.Sprintf("line %d MAVLink command %d is not supported", lineNumber, ints[3]), &seq))
	}
	if values[4] < -90 || values[4] > 90 {
		findings = append(findings, missionFinding("latitude_out_of_range", fmt.Sprintf("line %d latitude must be between -90 and 90", lineNumber), &seq))
	}
	if values[5] < -180 || values[5] > 180 {
		findings = append(findings, missionFinding("longitude_out_of_range", fmt.Sprintf("line %d longitude must be between -180 and 180", lineNumber), &seq))
	}
	if values[6] < -1000 || values[6] > 10000 {
		findings = append(findings, missionFinding("altitude_out_of_range", fmt.Sprintf("line %d altitude must be between -1000 and 10000 metres", lineNumber), &seq))
	}
	return domain.MissionItem{
		Sequence: ints[0], Current: ints[1] == 1, Frame: ints[2], Command: ints[3],
		Param1: values[0], Param2: values[1], Param3: values[2], Param4: values[3],
		LatitudeE7: int32(math.Round(values[4] * 1e7)), LongitudeE7: int32(math.Round(values[5] * 1e7)),
		AltitudeM: values[6], Autocontinue: autoContinue == 1,
	}, findings
}

func missionAltitudeCMRoundTrips(altitudeM float64) bool {
	altitudeCM := math.Round(altitudeM * 100)
	return altitudeCM >= -8388608 && altitudeCM <= 8388607 && float32(altitudeCM/100) == float32(altitudeM)
}

func missionFinding(code string, message string, sequence *int) domain.MissionValidationFinding {
	return domain.MissionValidationFinding{Severity: "error", Code: code, Message: message, Sequence: sequence}
}

func missionWarning(code string, message string) domain.MissionValidationFinding {
	return domain.MissionValidationFinding{Severity: "warning", Code: code, Message: message}
}

func canonicalMissionSHA(items []domain.MissionItem) (string, error) {
	plan := canonicalMissionPlan(items)
	canonical, err := proto.MarshalOptions{Deterministic: true}.Marshal(plan)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(canonical)
	return hex.EncodeToString(sum[:]), nil
}

func canonicalMissionPlan(items []domain.MissionItem) *agentv1.MissionPlan {
	plan := &agentv1.MissionPlan{SchemaVersion: 1, Items: make([]*agentv1.MissionItem, len(items))}
	for index, item := range items {
		plan.Items[index] = &agentv1.MissionItem{
			Sequence: uint32(item.Sequence), Frame: uint32(item.Frame), Command: uint32(item.Command),
			Current: item.Current, Autocontinue: item.Autocontinue,
			Param1: item.Param1, Param2: item.Param2, Param3: item.Param3, Param4: item.Param4,
			LatitudeE7: item.LatitudeE7, LongitudeE7: item.LongitudeE7, AltitudeM: item.AltitudeM,
		}
	}
	return plan
}

func sha256Hex(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
