package service

import (
	"context"
	"fmt"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

func preflightChecksForVersion(checks []domain.PreflightCheck, version int) []domain.PreflightCheck {
	filtered := make([]domain.PreflightCheck, 0, len(checks))
	for _, check := range checks {
		if check.IntentVersion == version {
			filtered = append(filtered, check)
		}
	}
	return filtered
}

func complianceFindingsForVersion(findings []domain.ComplianceFinding, version int) []domain.ComplianceFinding {
	filtered := make([]domain.ComplianceFinding, 0, len(findings))
	for _, finding := range findings {
		if finding.IntentVersion == version {
			filtered = append(filtered, finding)
		}
	}
	return filtered
}

func conformanceSummaryForVersion(ctx context.Context, store durable.Store, intent domain.OperationalIntent) (*domain.ConformanceSummary, error) {
	summaries, err := store.ListConformanceSummaries(ctx, intent.ID)
	if err != nil {
		return nil, fmt.Errorf("list conformance summaries: %w", err)
	}
	for _, summary := range summaries {
		if summary.IntentID == intent.ID && summary.IntentVersion == intent.Version {
			return &summary, nil
		}
	}
	return nil, nil
}
