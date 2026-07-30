package durable

import (
	"context"
	"fmt"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

// OperationalStore is the narrow durable boundary needed by the operational
// intent and deconfliction workflows.
type OperationalStore interface {
	CreateOperationalIntent(context.Context, domain.OperationalIntent) error
	UpdateOperationalIntent(context.Context, domain.OperationalIntent) error
	GetOperationalIntent(context.Context, string) (domain.OperationalIntent, error)
	GetOperationalIntentVersion(context.Context, string, int) (domain.OperationalIntent, error)
	ListOperationalIntents(context.Context, string) ([]domain.OperationalIntent, error)
	ListOperationalIntentVersions(context.Context, string) ([]domain.OperationalIntent, error)
	RecordOperationalVolume(context.Context, domain.OperationalVolume) error
	ReplaceOperationalVolumes(context.Context, string, int, []domain.OperationalVolume) error
	ListOperationalVolumes(context.Context, string) ([]domain.OperationalVolume, error)
	RecordConflictFinding(context.Context, domain.ConflictFinding) error
	ListConflictFindings(context.Context, string, int) ([]domain.ConflictFinding, error)
	ReplaceConflictFindings(context.Context, string, int, string, []domain.ConflictFinding) error
}

type OperationalIntentReplacer interface {
	ReplaceOperationalIntent(context.Context, int, domain.OperationalIntent, []domain.OperationalVolume) error
}

// WithOperationalStore keeps the existing scaffold store for unrelated
// records while making one store authoritative for the deconfliction slice.
type WithOperationalStore struct {
	Store
	operational OperationalStore
}

func UseOperationalStore(base Store, operational OperationalStore) *WithOperationalStore {
	return &WithOperationalStore{Store: base, operational: operational}
}

func (s *WithOperationalStore) CreateOperationalIntent(ctx context.Context, value domain.OperationalIntent) error {
	return s.operational.CreateOperationalIntent(ctx, value)
}

func (s *WithOperationalStore) UpdateOperationalIntent(ctx context.Context, value domain.OperationalIntent) error {
	return s.operational.UpdateOperationalIntent(ctx, value)
}

func (s *WithOperationalStore) GetOperationalIntent(ctx context.Context, id string) (domain.OperationalIntent, error) {
	return s.operational.GetOperationalIntent(ctx, id)
}

func (s *WithOperationalStore) GetOperationalIntentVersion(ctx context.Context, id string, version int) (domain.OperationalIntent, error) {
	return s.operational.GetOperationalIntentVersion(ctx, id, version)
}

func (s *WithOperationalStore) ListOperationalIntents(ctx context.Context, aircraftID string) ([]domain.OperationalIntent, error) {
	return s.operational.ListOperationalIntents(ctx, aircraftID)
}

func (s *WithOperationalStore) ListOperationalIntentVersions(ctx context.Context, id string) ([]domain.OperationalIntent, error) {
	return s.operational.ListOperationalIntentVersions(ctx, id)
}

func (s *WithOperationalStore) RecordOperationalVolume(ctx context.Context, value domain.OperationalVolume) error {
	return s.operational.RecordOperationalVolume(ctx, value)
}

func (s *WithOperationalStore) ReplaceOperationalVolumes(ctx context.Context, id string, version int, values []domain.OperationalVolume) error {
	return s.operational.ReplaceOperationalVolumes(ctx, id, version, values)
}

func (s *WithOperationalStore) ListOperationalVolumes(ctx context.Context, id string) ([]domain.OperationalVolume, error) {
	return s.operational.ListOperationalVolumes(ctx, id)
}

func (s *WithOperationalStore) RecordConflictFinding(ctx context.Context, value domain.ConflictFinding) error {
	return s.operational.RecordConflictFinding(ctx, value)
}

func (s *WithOperationalStore) ListConflictFindings(ctx context.Context, id string, version int) ([]domain.ConflictFinding, error) {
	return s.operational.ListConflictFindings(ctx, id, version)
}

func (s *WithOperationalStore) ReplaceConflictFindings(ctx context.Context, id string, version int, ruleVersion string, values []domain.ConflictFinding) error {
	return s.operational.ReplaceConflictFindings(ctx, id, version, ruleVersion, values)
}

func (s *WithOperationalStore) ReplaceOperationalIntent(ctx context.Context, expectedVersion int, intent domain.OperationalIntent, volumes []domain.OperationalVolume) error {
	replacer, ok := s.operational.(OperationalIntentReplacer)
	if !ok {
		return fmt.Errorf("operational store does not support atomic intent replacement")
	}
	return replacer.ReplaceOperationalIntent(ctx, expectedVersion, intent, volumes)
}
