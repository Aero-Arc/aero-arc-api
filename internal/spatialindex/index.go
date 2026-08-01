package spatialindex

import (
	"context"
	"fmt"
	"sync"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

type Query struct {
	ExcludeIntentID string
	Volumes         []domain.OperationalVolume
}

type Candidate struct {
	IntentID      string
	IntentVersion int
}

type CandidateFinder interface {
	FindCandidates(context.Context, Query) ([]Candidate, error)
}

// Index is a replaceable spatial projection. It contains only the data needed
// for candidate discovery; the durable store remains authoritative.
type Index interface {
	ID() string
	Rebuild(context.Context, []domain.OperationalVolume) error
	RecordVolume(context.Context, domain.OperationalVolume) error
	ReplaceVolumes(context.Context, string, int, []domain.OperationalVolume) error
	FindCandidates(context.Context, Query) ([]Candidate, error)
	Close()
}

// Projection serializes local writes with candidate reads. If an authoritative
// write commits but its projection fails, candidate reads fail closed until a
// successful rebuild (normally at process startup).
type Projection struct {
	index   Index
	mu      sync.RWMutex
	syncErr error
}

func NewProjection(index Index) *Projection {
	return &Projection{index: index}
}

func (p *Projection) ID() string {
	return p.index.ID()
}

func (p *Projection) Rebuild(ctx context.Context, volumes []domain.OperationalVolume) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.index.Rebuild(ctx, volumes); err != nil {
		p.syncErr = err
		return err
	}
	p.syncErr = nil
	return nil
}

func (p *Projection) Apply(authoritative func() error, project func(Index) error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.syncErr != nil {
		return fmt.Errorf("spatial projection is out of sync; rebuild required: %w", p.syncErr)
	}
	if err := authoritative(); err != nil {
		return err
	}
	if err := project(p.index); err != nil {
		p.syncErr = err
		return fmt.Errorf("update spatial projection after durable commit: %w", err)
	}
	return nil
}

func (p *Projection) FindCandidates(ctx context.Context, query Query) ([]Candidate, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	if p.syncErr != nil {
		return nil, fmt.Errorf("spatial projection is out of sync: %w", p.syncErr)
	}
	return p.index.FindCandidates(ctx, query)
}

func (p *Projection) Close() {
	p.index.Close()
}
