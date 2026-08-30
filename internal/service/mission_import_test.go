package service

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/registry"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
	replaymemory "github.com/Aero-Arc/aero-arc-api/internal/store/replay/memory"
	telemetrymemory "github.com/Aero-Arc/aero-arc-api/internal/store/telemetry/memory"
)

const validWPL110 = "QGC WPL 110\n" +
	"0\t1\t0\t16\t0\t0\t0\t0\t-35.3632620\t149.1652370\t0\t1\n" +
	"1\t0\t0\t22\t0\t0\t0\t0\t-35.3632620\t149.1652370\t20\t1\n" +
	"2\t0\t0\t16\t0\t0\t0\t0\t-35.3620000\t149.1660000\t25\t1\n" +
	"3\t0\t0\t21\t0\t0\t0\t0\t-35.3632620\t149.1652370\t0\t1\n"

type missionCoverageErrorStore struct {
	durable.Store
	err error
}

func (s missionCoverageErrorStore) CheckMissionCoverage(context.Context, domain.OperationalVolume, []domain.MissionItem) (durable.MissionCoverageResult, error) {
	return durable.MissionCoverageResult{}, s.err
}

func TestImportMissionIsIntentBoundImmutableAndIdempotent(t *testing.T) {
	svc, store := newMissionTestService(t)
	req := validMissionRequest(validWPL110)
	first, err := svc.ImportMission(context.Background(), "flight-1", "mission-request-1", req)
	if err != nil {
		t.Fatalf("ImportMission: %v", err)
	}
	if first.Replayed || first.Mission.Version != 1 || len(first.Mission.Items) != 3 {
		t.Fatalf("first result = %#v", first)
	}
	if first.Mission.OperatorID != "operator-1" || first.Mission.AircraftID != "aircraft-1" || first.Mission.IntentID != "intent-1" || first.Mission.IntentVersion != 2 {
		t.Fatalf("binding = %#v", first.Mission)
	}
	if len(first.Mission.SourceSHA256) != 64 || len(first.Mission.MissionDigest) != 64 || first.Mission.SourceSHA256 == first.Mission.MissionDigest {
		t.Fatalf("hashes = source %q mission %q", first.Mission.SourceSHA256, first.Mission.MissionDigest)
	}
	if first.Mission.MissionDigest != "4644193f31efc8212b3e84a9266646ed0e3f22748d21bbc89933aa84b765571b" {
		t.Fatalf("mission digest = %q; does not match schema-version 1 canonical bytes", first.Mission.MissionDigest)
	}
	if got := first.Mission.Items[0].LatitudeE7; got != -353632620 {
		t.Fatalf("latitude_e7 = %d", got)
	}
	for sequence, item := range first.Mission.Items {
		if item.Sequence != sequence || item.Current {
			t.Fatalf("canonical item %d = %#v; HOME must be excluded and canonical current must be false", sequence, item)
		}
	}
	if land := first.Mission.Items[2]; land.Command != 21 || land.Param4 != 1 {
		t.Fatalf("canonical LAND = %#v; want command 21 param4 +1", land)
	}

	replay, err := svc.ImportMission(context.Background(), "flight-1", "mission-request-1", req)
	if err != nil {
		t.Fatalf("idempotent replay: %v", err)
	}
	if !replay.Replayed || replay.Mission.ID != first.Mission.ID || replay.Mission.Version != first.Mission.Version {
		t.Fatalf("replay = %#v, first = %#v", replay, first)
	}

	changed := req
	changed.Source = strings.Replace(validWPL110, "149.1660000", "149.1670000", 1)
	if _, err := svc.ImportMission(context.Background(), "flight-1", "mission-request-1", changed); !errors.Is(err, durable.ErrIdempotencyConflict) {
		t.Fatalf("conflicting idempotency error = %v", err)
	}
	second, err := svc.ImportMission(context.Background(), "flight-1", "mission-request-2", changed)
	if err != nil || second.Mission.Version != 2 || second.Mission.ID == first.Mission.ID {
		t.Fatalf("second version = %#v, err=%v", second, err)
	}
	volumes, err := store.ListOperationalVolumes(context.Background(), "intent-1")
	if err != nil || len(volumes) != 1 || volumes[0].ID != "volume-1" {
		t.Fatalf("operational volumes changed during mission import: %#v, err=%v", volumes, err)
	}
	current, err := svc.GetCurrentMission(context.Background(), "flight-1")
	if err != nil || current.ID != second.Mission.ID {
		t.Fatalf("current = %#v, err=%v", current, err)
	}
	flight, err := store.GetFlightRecord(context.Background(), "flight-1")
	if err != nil {
		t.Fatal(err)
	}
	flight.Status = domain.FlightStatusActive
	if err := store.UpdateFlightRecord(context.Background(), flight, domain.FlightStatusPlanned); err != nil {
		t.Fatal(err)
	}
	lateReplay, err := svc.ImportMission(context.Background(), "flight-1", "mission-request-1", req)
	if err != nil || !lateReplay.Replayed || lateReplay.Mission.ID != first.Mission.ID {
		t.Fatalf("post-transition idempotent replay = %#v, err=%v", lateReplay, err)
	}
}

func TestCanonicalMissionDigestMatchesContractGoldenVector(t *testing.T) {
	digest, err := canonicalMissionSHA([]domain.MissionItem{{
		Sequence: 0, Frame: 0, Command: 16, Autocontinue: true,
		LatitudeE7: -353632620, LongitudeE7: 1491652370, AltitudeM: 20.1,
	}})
	if err != nil {
		t.Fatal(err)
	}
	if digest != "6efa96b36af29a800d53ee7d7baf57d4b24f00d9ce2b408327281e74824acf4f" {
		t.Fatalf("canonical digest = %q; does not match the published schema-version 1 golden vector", digest)
	}
}

func TestImportMissionRejectsMalformedAndUnsupportedWPL(t *testing.T) {
	tests := []struct {
		name   string
		source string
		code   string
	}{
		{name: "header", source: "QGC WPL 120\n", code: "invalid_header"},
		{name: "field count", source: "QGC WPL 110\n0\t1\t3\n", code: "invalid_field_count"},
		{name: "latitude", source: strings.Replace(validWPL110, "-35.3620000", "91", 1), code: "latitude_out_of_range"},
		{name: "longitude", source: strings.Replace(validWPL110, "149.1660000", "181", 1), code: "longitude_out_of_range"},
		{name: "altitude", source: strings.Replace(validWPL110, "\t25\t1", "\t10001\t1", 1), code: "altitude_out_of_range"},
		{name: "command", source: strings.Replace(validWPL110, "\t16\t", "\t177\t", 1), code: "unsupported_command"},
		{name: "float32 overflow", source: strings.Replace(validWPL110, "\t0\t0\t0\t0\t-35.3620000", "\t1e300\t0\t0\t0\t-35.3620000", 1), code: "number_out_of_float32_range"},
		{name: "nonzero parameter", source: strings.Replace(validWPL110, "\t0\t0\t0\t0\t-35.3620000", "\t1\t0\t0\t0\t-35.3620000", 1), code: "nonzero_parameters_unsupported"},
		{name: "underflowing nonzero parameter", source: strings.Replace(validWPL110, "\t0\t0\t0\t0\t-35.3620000", "\t1e-50\t0\t0\t0\t-35.3620000", 1), code: "nonzero_parameters_unsupported"},
		{name: "waypoint param4", source: strings.Replace(validWPL110, "2\t0\t0\t16\t0\t0\t0\t0", "2\t0\t0\t16\t0\t0\t0\t1", 1), code: "nonzero_parameters_unsupported"},
		{name: "land param4 unsupported", source: strings.Replace(validWPL110, "3\t0\t0\t21\t0\t0\t0\t0", "3\t0\t0\t21\t0\t0\t0\t2", 1), code: "nonzero_parameters_unsupported"},
		{name: "autocontinue false", source: strings.Replace(validWPL110, "\t25\t1\n3\t", "\t25\t0\n3\t", 1), code: "autocontinue_required"},
		{name: "altitude not centimeter roundtrippable", source: strings.Replace(validWPL110, "\t25\t1\n3\t", "\t25.123\t1\n3\t", 1), code: "altitude_not_centimeter_roundtrippable"},
		{name: "altitude changed by ArduPilot float truncation", source: strings.Replace(validWPL110, "\t25\t1\n3\t", "\t16.8\t1\n3\t", 1), code: "altitude_not_centimeter_roundtrippable"},
		{name: "frame", source: strings.Replace(validWPL110, "2\t0\t0\t16", "2\t0\t3\t16", 1), code: "unsupported_frame"},
		{name: "sequence", source: strings.Replace(validWPL110, "2\t0\t0\t16", "9\t0\t0\t16", 1), code: "non_contiguous_sequence"},
		{name: "invalid home command", source: strings.Replace(validWPL110, "0\t1\t0\t16", "0\t1\t0\t22", 1), code: "invalid_home_metadata"},
		{name: "invalid home current", source: strings.Replace(validWPL110, "0\t1\t0\t16", "0\t0\t0\t16", 1), code: "invalid_home_metadata"},
		{name: "operational current", source: strings.Replace(validWPL110, "2\t0\t0\t16", "2\t1\t0\t16", 1), code: "operational_item_marked_current"},
		{name: "home only", source: strings.Split(validWPL110, "1\t0\t0\t22")[0], code: "empty_mission"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc, _ := newMissionTestService(t)
			_, err := svc.ImportMission(context.Background(), "flight-1", "key-"+strings.ReplaceAll(tt.name, " ", "-"), validMissionRequest(tt.source))
			var validationErr MissionValidationError
			if !errors.As(err, &validationErr) {
				t.Fatalf("error = %v, want MissionValidationError", err)
			}
			found := false
			for _, finding := range validationErr.Findings {
				found = found || finding.Code == tt.code
			}
			if !found {
				t.Fatalf("findings = %#v, want code %s", validationErr.Findings, tt.code)
			}
		})
	}
}

func TestMissionAltitudeCMRoundTripsArduPilotFloatSemantics(t *testing.T) {
	for _, test := range []struct {
		altitudeM float64
		want      bool
	}{
		{altitudeM: 0, want: true},
		{altitudeM: 20, want: true},
		{altitudeM: 16.8, want: false},
		{altitudeM: -16.8, want: false},
		{altitudeM: 25.123, want: false},
	} {
		if got := missionAltitudeCMRoundTrips(test.altitudeM); got != test.want {
			t.Errorf("missionAltitudeCMRoundTrips(%v) = %t, want %t", test.altitudeM, got, test.want)
		}
	}
}

func TestMissionAltitudeCMInt32Boundary(t *testing.T) {
	const upper = float32(1 << 31)
	lower := -upper
	for _, test := range []struct {
		name     string
		scaledCM float32
		want     bool
	}{
		{name: "inclusive lower bound", scaledCM: lower, want: true},
		{name: "below lower bound", scaledCM: math.Nextafter32(lower, float32(math.Inf(-1))), want: false},
		{name: "largest float32 below upper bound", scaledCM: math.Nextafter32(upper, lower), want: true},
		{name: "exclusive upper bound", scaledCM: upper, want: false},
		{name: "positive infinity", scaledCM: float32(math.Inf(1)), want: false},
		{name: "negative infinity", scaledCM: float32(math.Inf(-1)), want: false},
		{name: "not a number", scaledCM: float32(math.NaN()), want: false},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := missionAltitudeCMInInt32Range(test.scaledCM); got != test.want {
				t.Errorf("missionAltitudeCMInInt32Range(%v) = %t, want %t", test.scaledCM, got, test.want)
			}
		})
	}
}

func TestImportMissionCanonicalizesLandParam4BeforeDigest(t *testing.T) {
	svc, _ := newMissionTestService(t)
	fromZero, err := svc.ImportMission(context.Background(), "flight-1", "land-param4-zero", validMissionRequest(validWPL110))
	if err != nil {
		t.Fatal(err)
	}
	sourceOne := strings.Replace(validWPL110, "3\t0\t0\t21\t0\t0\t0\t0", "3\t0\t0\t21\t0\t0\t0\t1", 1)
	fromOne, err := svc.ImportMission(context.Background(), "flight-1", "land-param4-one", validMissionRequest(sourceOne))
	if err != nil {
		t.Fatal(err)
	}
	zeroLand := fromZero.Mission.Items[len(fromZero.Mission.Items)-1]
	oneLand := fromOne.Mission.Items[len(fromOne.Mission.Items)-1]
	if zeroLand.Param4 != 1 || oneLand.Param4 != 1 {
		t.Fatalf("canonical LAND params = zero-source:%v one-source:%v", zeroLand.Param4, oneLand.Param4)
	}
	if fromZero.Mission.MissionDigest != fromOne.Mission.MissionDigest || fromZero.Mission.SourceSHA256 == fromOne.Mission.SourceSHA256 {
		t.Fatalf("LAND param4 source normalization must preserve canonical digest and exact source distinction: zero=%#v one=%#v", fromZero.Mission, fromOne.Mission)
	}
}

func TestImportMissionCanonicalizesNegativeZeroParameters(t *testing.T) {
	svc, _ := newMissionTestService(t)
	source := strings.Replace(validWPL110, "2\t0\t0\t16\t0\t0\t0\t0", "2\t0\t0\t16\t-0\t-0\t-0\t-0", 1)
	result, err := svc.ImportMission(context.Background(), "flight-1", "negative-zero-params", validMissionRequest(source))
	if err != nil {
		t.Fatal(err)
	}
	item := result.Mission.Items[1]
	for index, value := range []float64{item.Param1, item.Param2, item.Param3, item.Param4} {
		if value != 0 || math.Signbit(value) {
			t.Fatalf("canonical param%d = %v signbit=%v; want positive zero", index+1, value, math.Signbit(value))
		}
	}
}

func TestImportMissionCanonicalizesCentimeterRoundtrippableAltitudeBeforeDigest(t *testing.T) {
	svc, _ := newMissionTestService(t)
	source := strings.Replace(validWPL110, "\t-35.3620000\t149.1660000\t25\t1", "\t-35.3620000\t149.1660000\t25.1\t1", 1)
	result, err := svc.ImportMission(context.Background(), "flight-1", "float32-canonical", validMissionRequest(source))
	if err != nil {
		t.Fatal(err)
	}
	item := result.Mission.Items[1]
	if item.Param1 != 0 || item.Param2 != 0 || item.Param3 != 0 || item.Param4 != 0 || item.AltitudeM != float64(float32(25.1)) {
		t.Fatalf("canonical float32 mission item = %#v", item)
	}
}

func TestImportMissionExcludesHomeMetadataFromAuthorizedRouteChecks(t *testing.T) {
	svc, _ := newMissionTestService(t)
	baseline, err := svc.ImportMission(context.Background(), "flight-1", "baseline-home", validMissionRequest(validWPL110))
	if err != nil {
		t.Fatal(err)
	}
	source := strings.Replace(
		validWPL110,
		"-35.3632620\t149.1652370\t0\t1\n1\t0\t0\t22",
		"0\t0\t500\t1\n1\t0\t0\t22",
		1,
	)
	result, err := svc.ImportMission(context.Background(), "flight-1", "home-outside-authority", validMissionRequest(source))
	if err != nil {
		t.Fatalf("HOME metadata outside the operational volume must not reject import: %v", err)
	}
	if len(result.Mission.Items) != 3 || result.Mission.Items[0].LatitudeE7 != -353632620 {
		t.Fatalf("canonical operational items unexpectedly include HOME: %#v", result.Mission.Items)
	}
	if result.Mission.MissionDigest != baseline.Mission.MissionDigest || result.Mission.SourceSHA256 == baseline.Mission.SourceSHA256 {
		t.Fatalf("HOME-only change must preserve mission digest but change exact source hash: baseline=%#v changed=%#v", baseline.Mission, result.Mission)
	}
}

func TestImportMissionRejectsBindingAndLifecycleMismatch(t *testing.T) {
	svc, store := newMissionTestService(t)
	req := validMissionRequest(validWPL110)
	req.IntentVersion = 1
	if _, err := svc.ImportMission(context.Background(), "flight-1", "binding-key", req); !errors.Is(err, ErrValidation) {
		t.Fatalf("binding mismatch error = %v", err)
	}

	flight, err := store.GetFlightRecord(context.Background(), "flight-1")
	if err != nil {
		t.Fatal(err)
	}
	flight.Status = domain.FlightStatusActive
	if err := store.UpdateFlightRecord(context.Background(), flight, domain.FlightStatusPlanned); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.ImportMission(context.Background(), "flight-1", "active-key", validMissionRequest(validWPL110)); !errors.Is(err, ErrInvalidTransition) {
		t.Fatalf("active-flight import error = %v", err)
	}
}

func TestImportMissionRejectsItemsAndSegmentsOutsideIntentVolume(t *testing.T) {
	t.Run("item outside", func(t *testing.T) {
		svc, _ := newMissionTestService(t)
		source := strings.Replace(validWPL110, "149.1660000", "149.1900000", 1)
		_, err := svc.ImportMission(context.Background(), "flight-1", "outside-item", validMissionRequest(source))
		assertMissionFinding(t, err, "mission_item_outside_authorized_volume")
	})

	t.Run("altitude outside", func(t *testing.T) {
		svc, _ := newMissionTestService(t)
		source := strings.Replace(validWPL110, "\t25\t1", "\t121\t1", 1)
		_, err := svc.ImportMission(context.Background(), "flight-1", "outside-altitude", validMissionRequest(source))
		assertMissionFinding(t, err, "mission_altitude_outside_authorized_volume")
	})

	t.Run("concave segment exits volume", func(t *testing.T) {
		svc, store := newMissionTestService(t)
		intent, err := store.GetOperationalIntentVersion(context.Background(), "intent-1", 2)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.RecordOperationalVolume(context.Background(), domain.OperationalVolume{
			ID: "volume-1", IntentID: intent.ID, IntentVersion: intent.Version,
			GeoJSON:      `{"type":"Polygon","coordinates":[[[0,0],[4,0],[4,4],[3,4],[3,1],[1,1],[1,4],[0,4],[0,0]]]}`,
			MinAltitudeM: 0, MaxAltitudeM: 100, AltitudeRef: domain.AltitudeReferenceMSL,
			StartsAt: intent.PlannedStartAt, EndsAt: intent.PlannedEndAt,
		}); err != nil {
			t.Fatal(err)
		}
		source := "QGC WPL 110\n" +
			"0\t1\t0\t16\t0\t0\t0\t0\t0\t0\t0\t1\n" +
			"1\t0\t0\t22\t0\t0\t0\t0\t3\t0.5\t20\t1\n" +
			"2\t0\t0\t21\t0\t0\t0\t0\t3\t3.5\t20\t1\n"
		_, err = svc.ImportMission(context.Background(), "flight-1", "outside-segment", validMissionRequest(source))
		assertMissionFinding(t, err, "mission_segment_outside_authorized_volume")
	})
}

func TestImportMissionClassifiesCoverageErrorsWithoutLeakingDependencies(t *testing.T) {
	t.Run("dependency failure propagates", func(t *testing.T) {
		svc, store := newMissionTestService(t)
		svc.durable = missionCoverageErrorStore{Store: store, err: context.DeadlineExceeded}
		_, err := svc.ImportMission(context.Background(), "flight-1", "coverage-dependency-error", validMissionRequest(validWPL110))
		var validationErr MissionValidationError
		if !errors.Is(err, context.DeadlineExceeded) || errors.As(err, &validationErr) {
			t.Fatalf("coverage dependency error = %v", err)
		}
	})

	t.Run("invalid geometry is a generic finding", func(t *testing.T) {
		svc, store := newMissionTestService(t)
		detail := "sensitive geometry parser detail"
		svc.durable = missionCoverageErrorStore{Store: store, err: fmt.Errorf("%w: %s", durable.ErrInvalidMissionCoverageGeometry, detail)}
		_, err := svc.ImportMission(context.Background(), "flight-1", "coverage-geometry-error", validMissionRequest(validWPL110))
		var validationErr MissionValidationError
		if !errors.As(err, &validationErr) || len(validationErr.Findings) != 1 || validationErr.Findings[0].Code != "authorized_geometry_not_evaluable" || strings.Contains(err.Error(), detail) {
			t.Fatalf("coverage geometry error = %#v err=%v", validationErr, err)
		}
	})
}

func TestImportMissionRejectsAmbiguousOrIncompatibleIntentVolumes(t *testing.T) {
	t.Run("multiple volumes", func(t *testing.T) {
		svc, store := newMissionTestService(t)
		intent, err := store.GetOperationalIntentVersion(context.Background(), "intent-1", 2)
		if err != nil {
			t.Fatal(err)
		}
		if err := store.RecordOperationalVolume(context.Background(), domain.OperationalVolume{
			ID: "volume-2", IntentID: intent.ID, IntentVersion: intent.Version, Sequence: 1,
			GeoJSON:      `{"type":"Polygon","coordinates":[[[149.15,-35.37],[149.18,-35.37],[149.18,-35.35],[149.15,-35.35],[149.15,-35.37]]]}`,
			MinAltitudeM: 0, MaxAltitudeM: 120, AltitudeRef: domain.AltitudeReferenceMSL,
			StartsAt: intent.PlannedStartAt, EndsAt: intent.PlannedEndAt,
		}); err != nil {
			t.Fatal(err)
		}
		_, err = svc.ImportMission(context.Background(), "flight-1", "multiple-volumes", validMissionRequest(validWPL110))
		assertMissionFinding(t, err, "unsupported_volume_topology")
	})

	t.Run("relative altitude volume", func(t *testing.T) {
		svc, store := newMissionTestService(t)
		volumes, err := store.ListOperationalVolumes(context.Background(), "intent-1")
		if err != nil || len(volumes) != 1 {
			t.Fatalf("volumes = %#v, err=%v", volumes, err)
		}
		volume := volumes[0]
		volume.AltitudeRef = domain.AltitudeReferenceRelative
		if err := store.RecordOperationalVolume(context.Background(), volume); err != nil {
			t.Fatal(err)
		}
		_, err = svc.ImportMission(context.Background(), "flight-1", "relative-volume", validMissionRequest(validWPL110))
		assertMissionFinding(t, err, "unsupported_altitude_reference")
	})
}

func assertMissionFinding(t *testing.T, err error, code string) {
	t.Helper()
	var validationErr MissionValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("error = %v, want MissionValidationError", err)
	}
	for _, finding := range validationErr.Findings {
		if finding.Code == code {
			return
		}
	}
	t.Fatalf("findings = %#v, want %s", validationErr.Findings, code)
}

func newMissionTestService(t *testing.T) (*FleetService, *durablememory.Store) {
	t.Helper()
	ctx := context.Background()
	store := durablememory.NewStore()
	now := time.Date(2026, 8, 26, 12, 0, 0, 0, time.UTC)
	if err := store.CreateAircraft(ctx, domain.Aircraft{ID: "aircraft-1", OperatorID: "operator-1", AgentID: "agent-1"}); err != nil {
		t.Fatal(err)
	}
	intent := domain.OperationalIntent{
		ID: "intent-1", Version: 2, OperatorID: "operator-1", AircraftID: "aircraft-1",
		Status: domain.IntentStatusAccepted, PlannedStartAt: now, PlannedEndAt: now.Add(time.Hour), UpdatedAt: now,
	}
	if err := store.CreateOperationalIntent(ctx, intent); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordOperationalVolume(ctx, domain.OperationalVolume{
		ID: "volume-1", IntentID: intent.ID, IntentVersion: intent.Version, Sequence: 0,
		GeoJSON:      `{"type":"Polygon","coordinates":[[[149.15,-35.37],[149.18,-35.37],[149.18,-35.35],[149.15,-35.35],[149.15,-35.37]]]}`,
		MinAltitudeM: 0, MaxAltitudeM: 120, AltitudeRef: domain.AltitudeReferenceMSL,
		StartsAt: intent.PlannedStartAt, EndsAt: intent.PlannedEndAt, CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatal(err)
	}
	flight := domain.FlightRecord{
		ID: "flight-1", OperatorID: "operator-1", AircraftID: "aircraft-1", IntentID: "intent-1",
		IntentVersion: 2, Status: domain.FlightStatusPlanned,
	}
	if err := store.CreateFlightRecord(ctx, flight); err != nil {
		t.Fatal(err)
	}
	svc := NewFleetService(store, telemetrymemory.NewStore(), replaymemory.NewStore(), registry.NewMemoryClient())
	svc.now = func() time.Time { return now }
	return svc, store
}

func validMissionRequest(source string) ImportMissionRequest {
	return ImportMissionRequest{
		SourceFormat: domain.MissionSourceFormatQGCWPL110, Source: source,
		AircraftID: "aircraft-1", IntentID: "intent-1", IntentVersion: 2,
	}
}
