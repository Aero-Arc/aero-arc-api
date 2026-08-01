package spatialindex

import (
	"context"
	"errors"
	"testing"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

func TestProjectionFailsClosedUntilRebuilt(t *testing.T) {
	ctx := context.Background()
	index := &projectionTestIndex{}
	projection := NewProjection(index)
	if projection.ID() != "test" {
		t.Fatalf("ID = %q", projection.ID())
	}
	if err := projection.Rebuild(ctx, nil); err != nil {
		t.Fatal(err)
	}
	authoritativeCalled := false
	index.writeErr = errors.New("index unavailable")
	err := projection.Apply(
		func() error { authoritativeCalled = true; return nil },
		func(index Index) error { return index.RecordVolume(ctx, domain.OperationalVolume{}) },
	)
	if err == nil || !authoritativeCalled {
		t.Fatalf("Apply error = %v, authoritative called = %t", err, authoritativeCalled)
	}
	if _, err := projection.FindCandidates(ctx, Query{}); err == nil {
		t.Fatal("expected candidate reads to fail closed")
	}
	authoritativeCalled = false
	if err := projection.Apply(func() error { authoritativeCalled = true; return nil }, func(Index) error { return nil }); err == nil {
		t.Fatal("expected writes to require a rebuild")
	}
	if authoritativeCalled {
		t.Fatal("authoritative write ran while projection was unhealthy")
	}

	index.writeErr = nil
	if err := projection.Rebuild(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := projection.Apply(func() error { return nil }, func(Index) error { return nil }); err != nil {
		t.Fatal(err)
	}
	if _, err := projection.FindCandidates(ctx, Query{}); err != nil {
		t.Fatal(err)
	}
	projection.Close()
	if !index.closed {
		t.Fatal("underlying index was not closed")
	}
}

type projectionTestIndex struct {
	writeErr error
	closed   bool
}

func (*projectionTestIndex) ID() string { return "test" }
func (i *projectionTestIndex) Close()   { i.closed = true }
func (i *projectionTestIndex) Rebuild(context.Context, []domain.OperationalVolume) error {
	return i.writeErr
}
func (i *projectionTestIndex) RecordVolume(context.Context, domain.OperationalVolume) error {
	return i.writeErr
}
func (i *projectionTestIndex) ReplaceVolumes(context.Context, string, int, []domain.OperationalVolume) error {
	return i.writeErr
}
func (*projectionTestIndex) FindCandidates(context.Context, Query) ([]Candidate, error) {
	return nil, nil
}
