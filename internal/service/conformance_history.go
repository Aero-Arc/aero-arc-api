package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	conformancev1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/conformance/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ErrConformanceHistoryUnavailable distinguishes missing history service from an empty read.
var ErrConformanceHistoryUnavailable = errors.New("durable conformance history is unavailable")

// ConformanceHistoryClient is the read-only service boundary; the API never queries Conformance's database.
type ConformanceHistoryClient interface {
	ListConformanceEvents(context.Context, *conformancev1.ListConformanceEventsRequest, ...grpc.CallOption) (*conformancev1.ListConformanceEventsResponse, error)
}

// ConformanceHistoryEvent carries immutable incident evidence, separate from live summaries.
type ConformanceHistoryEvent struct {
	ID                   string     `json:"id"`
	AssignmentID         string     `json:"assignment_id"`
	AssignmentGeneration uint64     `json:"assignment_generation"`
	IntentID             string     `json:"intent_id"`
	IntentVersion        uint32     `json:"intent_version"`
	AircraftID           string     `json:"aircraft_id"`
	FlightID             string     `json:"flight_id"`
	IncidentID           string     `json:"incident_id"`
	Transition           string     `json:"transition"`
	ViolationType        string     `json:"violation_type"`
	ObservedAt           time.Time  `json:"observed_at"`
	DeviationM           *float64   `json:"deviation_m,omitempty"`
	FrameID              string     `json:"frame_id"`
	EvaluationRevision   uint64     `json:"evaluation_revision"`
	PlannedStartAt       *time.Time `json:"planned_start_at,omitempty"`
	PlannedEndAt         *time.Time `json:"planned_end_at,omitempty"`
}

// ConformanceHistoryPage is a newest-first, bounded, service-owned event page.
type ConformanceHistoryPage struct {
	Events        []ConformanceHistoryEvent `json:"events"`
	NextPageToken string                    `json:"next_page_token,omitempty"`
}

// WithConformanceHistory configures the read-only history dependency; nil disables history.
func (s *FleetService) WithConformanceHistory(client ConformanceHistoryClient) *FleetService {
	s.conformanceHistory = client
	return s
}

// GetConformanceHistory resolves the durable intent before reading its assignment history.
//
// Parameters:
//   - ctx: bounds intent lookup and the downstream RPC.
//   - intentID: identifies an existing durable intent; it determines the assignment scope.
//   - query: carries optional generation, time bounds, and continuation. Its assignment ID is not trusted.
//
// Returns:
//   - page: persisted transition evidence, including resolved and older-generation events.
//   - error: reports missing intent, invalid query, or history unavailability without hiding live state.
func (s *FleetService) GetConformanceHistory(ctx context.Context, intentID string, query *conformancev1.ListConformanceEventsRequest) (ConformanceHistoryPage, error) {
	intent, err := s.durable.GetOperationalIntent(ctx, intentID)
	if err != nil {
		return ConformanceHistoryPage{}, fmt.Errorf("get history intent: %w", err)
	}
	if s.conformanceHistory == nil {
		return ConformanceHistoryPage{}, ErrConformanceHistoryUnavailable
	}
	request := &conformancev1.ListConformanceEventsRequest{AssignmentId: intent.ID, AssignmentGeneration: query.GetAssignmentGeneration(), From: query.GetFrom(), Until: query.GetUntil(), PageSize: query.GetPageSize(), PageToken: query.GetPageToken()}
	response, err := s.conformanceHistory.ListConformanceEvents(ctx, request)
	if err != nil {
		if status.Code(err) == codes.InvalidArgument {
			return ConformanceHistoryPage{}, fmt.Errorf("%w: invalid conformance history filters or page token", ErrValidation)
		}
		return ConformanceHistoryPage{}, ErrConformanceHistoryUnavailable
	}
	if response == nil {
		return ConformanceHistoryPage{}, ErrConformanceHistoryUnavailable
	}
	result := ConformanceHistoryPage{Events: make([]ConformanceHistoryEvent, 0, len(response.Events)), NextPageToken: response.NextPageToken}
	for _, e := range response.Events {
		if e == nil || e.AssignmentId != intent.ID || e.IntentId != intent.ID || (request.AssignmentGeneration != 0 && e.AssignmentGeneration != request.AssignmentGeneration) || e.ObservedAt == nil || e.ObservedAt.CheckValid() != nil {
			return ConformanceHistoryPage{}, ErrConformanceHistoryUnavailable
		}
		kind := strings.ToLower(strings.TrimPrefix(e.ViolationType.String(), "VIOLATION_TYPE_"))
		result.Events = append(result.Events, ConformanceHistoryEvent{ID: e.EventId, AssignmentID: e.AssignmentId, AssignmentGeneration: e.AssignmentGeneration, IntentID: e.IntentId, IntentVersion: e.IntentVersion, AircraftID: e.AircraftId, FlightID: e.FlightId, IncidentID: e.IncidentId, Transition: e.Transition, ViolationType: kind, ObservedAt: e.ObservedAt.AsTime(), DeviationM: e.DeviationM, FrameID: e.FrameId, EvaluationRevision: e.EvaluationRevision})
		last := &result.Events[len(result.Events)-1]
		if e.PlannedStartAt != nil {
			if e.PlannedStartAt.CheckValid() != nil {
				return ConformanceHistoryPage{}, ErrConformanceHistoryUnavailable
			}
			v := e.PlannedStartAt.AsTime()
			last.PlannedStartAt = &v
		}
		if e.PlannedEndAt != nil {
			if e.PlannedEndAt.CheckValid() != nil {
				return ConformanceHistoryPage{}, ErrConformanceHistoryUnavailable
			}
			v := e.PlannedEndAt.AsTime()
			last.PlannedEndAt = &v
		}
	}
	return result, nil
}
