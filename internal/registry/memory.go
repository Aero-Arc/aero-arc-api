package registry

import (
	"context"
	"sort"
	"sync"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

type MemoryClient struct {
	mu     sync.RWMutex
	states map[string]domain.LiveAircraftState
}

func NewMemoryClient() *MemoryClient {
	return &MemoryClient{states: make(map[string]domain.LiveAircraftState)}
}

func (c *MemoryClient) SetLiveAircraftState(_ context.Context, state domain.LiveAircraftState) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.states[state.AircraftID] = state
	return nil
}

func (c *MemoryClient) GetLiveAircraftState(_ context.Context, aircraftID string) (*domain.LiveAircraftState, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	state, ok := c.states[aircraftID]
	if !ok {
		return nil, nil
	}
	return &state, nil
}

func (c *MemoryClient) ListLiveAircraftStates(_ context.Context) ([]domain.LiveAircraftState, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	states := make([]domain.LiveAircraftState, 0, len(c.states))
	for _, state := range c.states {
		states = append(states, state)
	}
	sort.Slice(states, func(i, j int) bool { return states[i].AircraftID < states[j].AircraftID })
	return states, nil
}
