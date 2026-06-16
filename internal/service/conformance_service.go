package service

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	"github.com/Aero-Arc/aero-arc-api/internal/store/telemetry"
)

type ConformanceService struct {
	durable   durable.Store
	telemetry telemetry.Store
	now       func() time.Time
}

type ConformanceEvaluation struct {
	Intent  domain.OperationalIntent  `json:"intent"`
	Summary domain.ConformanceSummary `json:"summary"`
	Events  []domain.ConformanceEvent `json:"events"`
}

type telemetryWriter interface {
	AddSample(ctx context.Context, sample domain.TelemetrySample) error
}

func NewConformanceService(durableStore durable.Store, telemetryStore telemetry.Store) *ConformanceService {
	return NewConformanceServiceWithClock(durableStore, telemetryStore, nil)
}

func NewConformanceServiceWithClock(durableStore durable.Store, telemetryStore telemetry.Store, now func() time.Time) *ConformanceService {
	if now == nil {
		now = func() time.Time { return time.Now().UTC() }
	}
	return &ConformanceService{durable: durableStore, telemetry: telemetryStore, now: now}
}

func (s *ConformanceService) EvaluateTelemetry(ctx context.Context, sample domain.TelemetrySample) (ConformanceEvaluation, error) {
	if sample.RecordedAt.IsZero() {
		sample.RecordedAt = s.now().UTC()
	}
	if writer, ok := s.telemetry.(telemetryWriter); ok {
		if err := writer.AddSample(ctx, sample); err != nil {
			return ConformanceEvaluation{}, fmt.Errorf("add telemetry sample: %w", err)
		}
	}

	intent, err := s.activeIntentForAircraft(ctx, sample.AircraftID, sample.RecordedAt)
	if err != nil {
		return ConformanceEvaluation{}, err
	}
	volumes, err := s.durable.ListOperationalVolumes(ctx, intent.ID)
	if err != nil {
		return ConformanceEvaluation{}, fmt.Errorf("list operational volumes: %w", err)
	}

	expectedVolumeID := ""
	inside := false
	for _, volume := range volumes {
		if expectedVolumeID == "" {
			expectedVolumeID = volume.ID
		}
		if sampleInsideVolume(sample, volume) {
			expectedVolumeID = volume.ID
			inside = true
			break
		}
	}

	score := 1.0
	status := domain.ConformanceStatusConforming
	reportability := domain.ReportabilityStatusNo
	events := make([]domain.ConformanceEvent, 0)
	if !inside {
		score = 0
		status = domain.ConformanceStatusNonConforming
		reportability = domain.ReportabilityStatusReview
		event := domain.ConformanceEvent{
			ID:               conformanceEventID(sample, intent),
			OperatorID:       intent.OperatorID,
			IntentID:         intent.ID,
			IntentVersion:    intent.Version,
			FlightID:         sample.FlightID,
			AircraftID:       sample.AircraftID,
			Severity:         domain.SeverityWarning,
			EventCode:        domain.ConformanceEventIntentExit,
			ExpectedVolumeID: expectedVolumeID,
			Message:          "telemetry sample is outside all active operational volumes",
			Latitude:         &sample.Latitude,
			Longitude:        &sample.Longitude,
			AltitudeM:        &sample.AltitudeM,
			AltitudeRef:      domain.AltitudeReferenceAGL,
			OccurredAt:       sample.RecordedAt.UTC(),
		}
		if err := s.durable.RecordConformanceEvent(ctx, event); err != nil {
			return ConformanceEvaluation{}, fmt.Errorf("record conformance event: %w", err)
		}
		events = append(events, event)
	}

	summary := domain.ConformanceSummary{
		ID:                  fmt.Sprintf("conformance-%s", intent.ID),
		OperatorID:          intent.OperatorID,
		IntentID:            intent.ID,
		IntentVersion:       intent.Version,
		FlightID:            sample.FlightID,
		AircraftID:          sample.AircraftID,
		Status:              status,
		Score:               &score,
		AlertCount:          len(events),
		ReportabilityStatus: reportability,
		UpdatedAt:           s.now().UTC(),
	}
	if existing, err := s.durable.GetConformanceSummary(ctx, intent.ID); err != nil {
		return ConformanceEvaluation{}, fmt.Errorf("get conformance summary: %w", err)
	} else if existing != nil && !inside {
		summary.AlertCount = existing.AlertCount + len(events)
	}
	if err := s.durable.UpsertConformanceSummary(ctx, summary); err != nil {
		return ConformanceEvaluation{}, fmt.Errorf("upsert conformance summary: %w", err)
	}

	return ConformanceEvaluation{Intent: intent, Summary: summary, Events: events}, nil
}

func (s *ConformanceService) GetIntentConformance(ctx context.Context, intentID string) (ConformanceEvaluation, error) {
	intent, err := s.durable.GetOperationalIntent(ctx, intentID)
	if err != nil {
		return ConformanceEvaluation{}, fmt.Errorf("get operational intent: %w", err)
	}
	summary, err := s.durable.GetConformanceSummary(ctx, intent.ID)
	if err != nil {
		return ConformanceEvaluation{}, fmt.Errorf("get conformance summary: %w", err)
	}
	events, err := s.durable.ListConformanceEvents(ctx, "")
	if err != nil {
		return ConformanceEvaluation{}, fmt.Errorf("list conformance events: %w", err)
	}
	filtered := make([]domain.ConformanceEvent, 0)
	for _, event := range events {
		if event.IntentID == intent.ID {
			filtered = append(filtered, event)
		}
	}
	if summary == nil {
		return ConformanceEvaluation{Intent: intent, Events: filtered}, nil
	}
	return ConformanceEvaluation{Intent: intent, Summary: *summary, Events: filtered}, nil
}

func (s *ConformanceService) activeIntentForAircraft(ctx context.Context, aircraftID string, recordedAt time.Time) (domain.OperationalIntent, error) {
	intents, err := s.durable.ListOperationalIntents(ctx, aircraftID)
	if err != nil {
		return domain.OperationalIntent{}, fmt.Errorf("list operational intents: %w", err)
	}
	for _, intent := range intents {
		if intent.Status != domain.IntentStatusActive {
			continue
		}
		if !recordedAt.Before(intent.PlannedStartAt) && !recordedAt.After(intent.PlannedEndAt) {
			return intent, nil
		}
	}
	for _, intent := range intents {
		if intent.Status == domain.IntentStatusActive {
			return intent, nil
		}
	}
	return domain.OperationalIntent{}, fmt.Errorf("%w: active operational intent for aircraft %s", durable.ErrNotFound, aircraftID)
}

func sampleInsideVolume(sample domain.TelemetrySample, volume domain.OperationalVolume) bool {
	if sample.RecordedAt.Before(volume.StartsAt) || sample.RecordedAt.After(volume.EndsAt) {
		return false
	}
	if sample.AltitudeM < volume.MinAltitudeM || sample.AltitudeM > volume.MaxAltitudeM {
		return false
	}
	if volume.GeoJSON == "" {
		return true
	}
	return pointInGeoJSONPolygon(sample.Longitude, sample.Latitude, []byte(volume.GeoJSON))
}

func conformanceEventID(sample domain.TelemetrySample, intent domain.OperationalIntent) string {
	if sample.ID != "" {
		return fmt.Sprintf("conformance-event-%s-intent-exit", sample.ID)
	}
	return fmt.Sprintf("conformance-event-%s-%d-intent-exit", intent.ID, sample.RecordedAt.UTC().UnixNano())
}

func pointInGeoJSONPolygon(lon, lat float64, raw []byte) bool {
	var payload struct {
		Type        string          `json:"type"`
		Geometry    json.RawMessage `json:"geometry"`
		Coordinates json.RawMessage `json:"coordinates"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return false
	}
	coords := payload.Coordinates
	if payload.Type == "Feature" {
		var geometry struct {
			Type        string          `json:"type"`
			Coordinates json.RawMessage `json:"coordinates"`
		}
		if err := json.Unmarshal(payload.Geometry, &geometry); err != nil || geometry.Type != "Polygon" {
			return false
		}
		coords = geometry.Coordinates
	} else if payload.Type != "Polygon" {
		return false
	}

	var polygon [][][]float64
	if err := json.Unmarshal(coords, &polygon); err != nil || len(polygon) == 0 {
		return false
	}
	return pointInRing(lon, lat, polygon[0])
}

func pointInRing(lon, lat float64, ring [][]float64) bool {
	if len(ring) < 3 {
		return false
	}
	inside := false
	j := len(ring) - 1
	for i := range ring {
		if len(ring[i]) < 2 || len(ring[j]) < 2 {
			j = i
			continue
		}
		xi, yi := ring[i][0], ring[i][1]
		xj, yj := ring[j][0], ring[j][1]
		intersects := ((yi > lat) != (yj > lat)) && (lon < (xj-xi)*(lat-yi)/(yj-yi)+xi)
		if intersects {
			inside = !inside
		}
		j = i
	}
	return inside
}
