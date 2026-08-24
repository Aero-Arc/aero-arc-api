package registry

import (
	"context"
	"testing"
	"time"

	conformancev1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/conformance/v1"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestMemoryClientConformanceCursorAndBatchContract(t *testing.T) {
	ctx := context.Background()
	client := NewMemoryClient()
	summary := &conformancev1.ConformanceSummary{
		AssignmentId: "assignment-b", AssignmentGeneration: 2, EvaluationRevision: 3,
		EvaluationId: "evaluation-3", AircraftId: "aircraft-1", FlightId: "flight-1",
		IntentId: "assignment-b", IntentVersion: 1,
		Condition:        conformancev1.ConformanceCondition_CONFORMANCE_CONDITION_CONFORMING,
		MonitoringStatus: conformancev1.MonitoringStatus_MONITORING_STATUS_CURRENT,
		RecordingStatus:  conformancev1.RecordingStatus_RECORDING_STATUS_CONFIRMED,
		ObservedAt:       timestamppb.New(time.Date(2026, 8, 24, 6, 0, 0, 0, time.UTC)), FrameId: "frame-3",
	}
	first, err := client.PublishConformanceSummary(ctx, &registryv1.PublishConformanceSummaryRequest{Summary: summary})
	if err != nil || first.GetDisposition() != registryv1.ConformancePublishDisposition_CONFORMANCE_PUBLISH_DISPOSITION_APPLIED {
		t.Fatalf("first publish = %#v, %v", first, err)
	}
	retry, err := client.PublishConformanceSummary(ctx, &registryv1.PublishConformanceSummaryRequest{Summary: summary})
	if err != nil || retry.GetDisposition() != registryv1.ConformancePublishDisposition_CONFORMANCE_PUBLISH_DISPOSITION_IDEMPOTENT {
		t.Fatalf("retry publish = %#v, %v", retry, err)
	}
	stale := proto.Clone(summary).(*conformancev1.ConformanceSummary)
	stale.EvaluationRevision--
	if _, err := client.PublishConformanceSummary(ctx, &registryv1.PublishConformanceSummaryRequest{Summary: stale}); status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stale publish error = %v, want FailedPrecondition", err)
	}

	summary.FrameId = "mutated-after-publish"
	batch, err := client.BatchGetConformanceSummaries(ctx, &registryv1.BatchGetConformanceSummariesRequest{
		AssignmentIds: []string{"missing", "assignment-b", "assignment-b"},
	})
	if err != nil {
		t.Fatalf("BatchGetConformanceSummaries returned error: %v", err)
	}
	if len(batch.GetProjections()) != 1 || batch.GetProjections()[0].GetSummary().GetFrameId() != "frame-3" {
		t.Fatalf("batch projections = %#v, want defensive assignment-b copy", batch.GetProjections())
	}
	if len(batch.GetMissingAssignmentIds()) != 1 || batch.GetMissingAssignmentIds()[0] != "missing" {
		t.Fatalf("batch missing = %v, want missing", batch.GetMissingAssignmentIds())
	}
}
