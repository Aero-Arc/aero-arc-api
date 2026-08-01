package durable

import (
	"context"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/spatialindex"
)

// WithSpatialIndex keeps Store authoritative and updates a spatial projection
// synchronously after each operational-volume commit.
type WithSpatialIndex struct {
	Store
	projection *spatialindex.Projection
}

func UseSpatialIndex(store Store, projection *spatialindex.Projection) *WithSpatialIndex {
	return &WithSpatialIndex{Store: store, projection: projection}
}

func (s *WithSpatialIndex) RecordOperationalVolume(ctx context.Context, volume domain.OperationalVolume) error {
	return s.projection.Apply(
		func() error { return s.Store.RecordOperationalVolume(ctx, volume) },
		func(index spatialindex.Index) error { return index.RecordVolume(ctx, volume) },
	)
}

func (s *WithSpatialIndex) ReplaceOperationalVolumes(ctx context.Context, id string, version int, volumes []domain.OperationalVolume) error {
	return s.projection.Apply(
		func() error { return s.Store.ReplaceOperationalVolumes(ctx, id, version, volumes) },
		func(index spatialindex.Index) error { return index.ReplaceVolumes(ctx, id, version, volumes) },
	)
}

func (s *WithSpatialIndex) ReplaceOperationalIntent(
	ctx context.Context,
	expectedVersion int,
	intent domain.OperationalIntent,
	volumes []domain.OperationalVolume,
) error {
	return s.projection.Apply(
		func() error { return s.Store.ReplaceOperationalIntent(ctx, expectedVersion, intent, volumes) },
		func(index spatialindex.Index) error {
			return index.ReplaceVolumes(ctx, intent.ID, intent.Version, volumes)
		},
	)
}
