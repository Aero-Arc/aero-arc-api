package preflight

import (
	"fmt"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

const ruleVersion = "demo.v1"

// Builder constructs PreflightCheck and ComplianceFinding records for one evaluation.
type Builder struct {
	snapshot Snapshot
	checks   []domain.PreflightCheck
	findings []domain.ComplianceFinding
	blocked  bool
}

func newBuilder(snapshot Snapshot) *Builder {
	return &Builder{snapshot: snapshot}
}

func (b *Builder) Checks() []domain.PreflightCheck {
	return b.checks
}

func (b *Builder) Findings() []domain.ComplianceFinding {
	return b.findings
}

func (b *Builder) Blocked() bool {
	return b.blocked
}

func (b *Builder) Clear(category domain.PreflightCheckCategory, key, source, requirementCode, summary string) {
	b.record(category, key, source, requirementCode, summary, "", domain.PreflightStatusClear, false)
}

func (b *Builder) Block(category domain.PreflightCheckCategory, key, source, requirementCode, summary, remediation string) {
	b.record(category, key, source, requirementCode, summary, remediation, domain.PreflightStatusBlocked, true)
	b.blocked = true
}

func (b *Builder) record(category domain.PreflightCheckCategory, key, source, requirementCode, summary, remediation string, status domain.PreflightStatus, blocking bool) {
	intent := b.snapshot.Intent
	check := domain.PreflightCheck{
		ID:              fmt.Sprintf("preflight-%s-v%d-%s", intent.ID, intent.Version, key),
		OperatorID:      intent.OperatorID,
		IntentID:        intent.ID,
		IntentVersion:   intent.Version,
		AircraftID:      intent.AircraftID,
		Category:        category,
		Source:          source,
		Status:          status,
		Summary:         summary,
		RequirementCode: requirementCode,
		RuleVersion:     ruleVersion,
		Blocking:        blocking,
		CapturedAt:      b.snapshot.Now,
	}
	b.checks = append(b.checks, check)
	if !blocking {
		b.findings = append(b.findings, domain.ComplianceFinding{
			ID:              fmt.Sprintf("finding-%s-v%d-%s", intent.ID, intent.Version, key),
			OperatorID:      intent.OperatorID,
			IntentID:        intent.ID,
			IntentVersion:   intent.Version,
			SubjectType:     "operational_intent",
			SubjectID:       intent.ID,
			RequirementCode: requirementCode,
			Status:          domain.ComplianceFindingPass,
			Severity:        domain.SeverityInfo,
			Blocking:        false,
			RuleVersion:     ruleVersion,
			Message:         summary,
			EvaluatedAt:     b.snapshot.Now,
		})
		return
	}
	b.findings = append(b.findings, domain.ComplianceFinding{
		ID:              fmt.Sprintf("finding-%s-v%d-%s", intent.ID, intent.Version, key),
		OperatorID:      intent.OperatorID,
		IntentID:        intent.ID,
		IntentVersion:   intent.Version,
		SubjectType:     "operational_intent",
		SubjectID:       intent.ID,
		RequirementCode: requirementCode,
		Status:          domain.ComplianceFindingFail,
		Severity:        domain.SeverityCritical,
		Blocking:        true,
		RuleVersion:     ruleVersion,
		Remediation:     remediation,
		Message:         summary,
		EvaluatedAt:     b.snapshot.Now,
	})
}
