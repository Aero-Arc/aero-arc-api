package registry

import (
	"context"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	conformancev1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/conformance/v1"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type MemoryClient struct {
	mu          sync.RWMutex
	relays      map[string]*registryv1.Relay
	agents      map[string]*registryv1.Agent
	placements  map[string]*registryv1.AgentPlacement
	conformance map[string]*registryv1.ConformanceProjection
}

// NewMemoryClient constructs an empty, concurrency-safe Registry client for
// development and service tests.
//
// Returns:
//   - client: stores cloned Relay, Agent, and placement records in process memory.
func NewMemoryClient() *MemoryClient {
	return &MemoryClient{
		relays:      make(map[string]*registryv1.Relay),
		agents:      make(map[string]*registryv1.Agent),
		placements:  make(map[string]*registryv1.AgentPlacement),
		conformance: make(map[string]*registryv1.ConformanceProjection),
	}
}

// PublishConformanceSummary validates and stores a defensive copy of the
// current projection behind its assignment-generation and evaluation-revision
// cursor. Exact retries are idempotent; stale or conflicting cursors fail.
//
// Parameters:
//   - ctx: controls cancellation before the in-memory mutation.
//   - req: contains the current conformance summary.
//   - options: are accepted for gRPC client compatibility and are not used.
//
// Returns:
//   - response: contains the applied or idempotent projection.
//   - error: reports cancellation, invalid identity, or a stale/conflicting cursor.
func (c *MemoryClient) PublishConformanceSummary(ctx context.Context, req *registryv1.PublishConformanceSummaryRequest, _ ...grpc.CallOption) (*registryv1.PublishConformanceSummaryResponse, error) {
	select {
	case <-ctx.Done():
		return nil, status.FromContextError(ctx.Err()).Err()
	default:
	}
	summary := req.GetSummary()
	if err := validateConformanceSummary(summary); err != nil {
		return nil, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	existing := c.conformance[summary.GetAssignmentId()]
	disposition := registryv1.ConformancePublishDisposition_CONFORMANCE_PUBLISH_DISPOSITION_APPLIED
	if current := existing.GetSummary(); current != nil {
		switch {
		case summary.GetAssignmentGeneration() < current.GetAssignmentGeneration(),
			summary.GetAssignmentGeneration() == current.GetAssignmentGeneration() && summary.GetEvaluationRevision() < current.GetEvaluationRevision():
			return nil, status.Error(codes.FailedPrecondition, "conformance cursor is stale")
		case summary.GetAssignmentGeneration() == current.GetAssignmentGeneration() && summary.GetEvaluationRevision() == current.GetEvaluationRevision():
			if !proto.Equal(summary, current) {
				return nil, status.Error(codes.FailedPrecondition, "conformance cursor content changed")
			}
			disposition = registryv1.ConformancePublishDisposition_CONFORMANCE_PUBLISH_DISPOSITION_IDEMPOTENT
		}
	}
	projection := &registryv1.ConformanceProjection{
		Summary:  proto.Clone(summary).(*conformancev1.ConformanceSummary),
		StoredAt: timestamppb.Now(),
	}
	c.conformance[summary.GetAssignmentId()] = projection
	return &registryv1.PublishConformanceSummaryResponse{
		Disposition: disposition,
		Projection:  cloneConformanceProjection(projection),
	}, nil
}

// GetConformanceSummary returns a defensive copy of one current in-memory
// conformance projection.
//
// Parameters:
//   - ctx: controls cancellation before lookup.
//   - req: identifies the assignment.
//   - options: are accepted for gRPC client compatibility and are not used.
//
// Returns:
//   - response: contains the current projection.
//   - error: reports cancellation, invalid identity, or a missing projection.
func (c *MemoryClient) GetConformanceSummary(ctx context.Context, req *registryv1.GetConformanceSummaryRequest, _ ...grpc.CallOption) (*registryv1.GetConformanceSummaryResponse, error) {
	select {
	case <-ctx.Done():
		return nil, status.FromContextError(ctx.Err()).Err()
	default:
	}
	assignmentID := strings.TrimSpace(req.GetAssignmentId())
	if assignmentID == "" {
		return nil, status.Error(codes.InvalidArgument, "assignment_id is required")
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	projection := c.conformance[assignmentID]
	if projection == nil {
		return nil, status.Error(codes.NotFound, "conformance summary not found")
	}
	return &registryv1.GetConformanceSummaryResponse{Projection: cloneConformanceProjection(projection)}, nil
}

// BatchGetConformanceSummaries returns defensive copies of current projections
// in deterministic assignment order and separately reports missing IDs.
//
// Parameters:
//   - ctx: controls cancellation before lookup.
//   - req: identifies one to 250 assignments; duplicate IDs are collapsed.
//   - options: are accepted for gRPC client compatibility and are not used.
//
// Returns:
//   - response: contains sorted projections and missing assignment IDs.
//   - error: reports cancellation or invalid request identifiers.
func (c *MemoryClient) BatchGetConformanceSummaries(ctx context.Context, req *registryv1.BatchGetConformanceSummariesRequest, _ ...grpc.CallOption) (*registryv1.BatchGetConformanceSummariesResponse, error) {
	select {
	case <-ctx.Done():
		return nil, status.FromContextError(ctx.Err()).Err()
	default:
	}
	if len(req.GetAssignmentIds()) == 0 || len(req.GetAssignmentIds()) > 250 {
		return nil, status.Error(codes.InvalidArgument, "one to 250 assignment_ids are required")
	}
	unique := make(map[string]struct{}, len(req.GetAssignmentIds()))
	for _, rawID := range req.GetAssignmentIds() {
		assignmentID := strings.TrimSpace(rawID)
		if assignmentID == "" {
			return nil, status.Error(codes.InvalidArgument, "assignment_ids cannot contain an empty value")
		}
		unique[assignmentID] = struct{}{}
	}
	assignmentIDs := make([]string, 0, len(unique))
	for assignmentID := range unique {
		assignmentIDs = append(assignmentIDs, assignmentID)
	}
	sort.Strings(assignmentIDs)

	c.mu.RLock()
	defer c.mu.RUnlock()
	response := &registryv1.BatchGetConformanceSummariesResponse{
		Projections:          make([]*registryv1.ConformanceProjection, 0, len(assignmentIDs)),
		MissingAssignmentIds: make([]string, 0),
	}
	for _, assignmentID := range assignmentIDs {
		projection := c.conformance[assignmentID]
		if projection == nil {
			response.MissingAssignmentIds = append(response.MissingAssignmentIds, assignmentID)
			continue
		}
		response.Projections = append(response.Projections, cloneConformanceProjection(projection))
	}
	return response, nil
}

func validateConformanceSummary(summary *conformancev1.ConformanceSummary) error {
	if summary == nil || strings.TrimSpace(summary.GetAssignmentId()) == "" || summary.GetAssignmentGeneration() == 0 || summary.GetEvaluationRevision() == 0 || strings.TrimSpace(summary.GetEvaluationId()) == "" || strings.TrimSpace(summary.GetIntentId()) == "" || strings.TrimSpace(summary.GetAircraftId()) == "" || strings.TrimSpace(summary.GetFlightId()) == "" || summary.GetIntentVersion() == 0 || summary.GetObservedAt() == nil || summary.GetObservedAt().CheckValid() != nil || strings.TrimSpace(summary.GetFrameId()) == "" {
		return status.Error(codes.InvalidArgument, "conformance summary identity and cursor are required")
	}
	if summary.GetCondition() == conformancev1.ConformanceCondition_CONFORMANCE_CONDITION_UNSPECIFIED || summary.GetMonitoringStatus() == conformancev1.MonitoringStatus_MONITORING_STATUS_UNSPECIFIED || summary.GetRecordingStatus() == conformancev1.RecordingStatus_RECORDING_STATUS_UNSPECIFIED {
		return status.Error(codes.InvalidArgument, "conformance status axes are required")
	}
	return nil
}

func cloneConformanceProjection(projection *registryv1.ConformanceProjection) *registryv1.ConformanceProjection {
	if projection == nil {
		return nil
	}
	return proto.Clone(projection).(*registryv1.ConformanceProjection)
}

var _ registryv1.AeroRegistryClient = (*MemoryClient)(nil)

// SetLiveAircraftState sets the selected MemoryClient state to the supplied value.
//
// Parameters:
//   - ctx: is accepted for interface compatibility; the in-memory operation completes synchronously.
//   - state: is the domain.LiveAircraftState value supplied to SetLiveAircraftState.
//
// Returns:
//   - error: reports validation, dependency, cancellation, or persistence failures.
func (c *MemoryClient) SetLiveAircraftState(_ context.Context, state domain.LiveAircraftState) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	agentID := state.AgentID
	if agentID == "" {
		return status.Error(codes.InvalidArgument, "agent_id is required")
	}
	heartbeatAt := state.LastHeartbeatAt
	if heartbeatAt.IsZero() && state.Connected {
		heartbeatAt = time.Now().UTC()
	}
	c.agents[agentID] = &registryv1.Agent{
		AgentId:             agentID,
		LastHeartbeatUnixMs: unixMillis(heartbeatAt),
	}
	if state.RelayID != "" {
		placementAt := state.PlacementLastUpdatedAt
		if placementAt.IsZero() {
			placementAt = heartbeatAt
		}
		c.placements[agentID] = &registryv1.AgentPlacement{
			AgentId:           agentID,
			RelayId:           state.RelayID,
			LastUpdatedUnixMs: unixMillis(placementAt),
		}
	} else {
		delete(c.placements, agentID)
	}
	return nil
}

// RegisterRelay validates and stores a cloned Relay record by identity.
//
// Parameters:
//   - ctx: is accepted for interface compatibility; the in-memory operation completes synchronously.
//   - req: contains the validated request payload.
//   - options: are accepted for gRPC client compatibility and are not used by the in-memory implementation.
//
// Returns:
//   - result: is the *registryv1.RegisterRelayResponse value produced by RegisterRelay.
//   - error: reports a missing Relay payload or identifier.
func (c *MemoryClient) RegisterRelay(_ context.Context, req *registryv1.RegisterRelayRequest, _ ...grpc.CallOption) (*registryv1.RegisterRelayResponse, error) {
	relay := req.GetRelay()
	if relay == nil || relay.GetRelayId() == "" {
		return nil, status.Error(codes.InvalidArgument, "relay_id is required")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.relays[relay.GetRelayId()] = cloneRelay(relay)
	return &registryv1.RegisterRelayResponse{}, nil
}

// HeartbeatRelay replaces the heartbeat timestamp of an existing in-memory Relay.
//
// Parameters:
//   - ctx: is accepted for interface compatibility; the in-memory operation completes synchronously.
//   - req: contains the validated request payload.
//   - options: are accepted for gRPC client compatibility and are not used by the in-memory implementation.
//
// Returns:
//   - result: is the *registryv1.HeartbeatRelayResponse value produced by HeartbeatRelay.
//   - error: reports an unknown Relay identifier.
func (c *MemoryClient) HeartbeatRelay(_ context.Context, req *registryv1.HeartbeatRelayRequest, _ ...grpc.CallOption) (*registryv1.HeartbeatRelayResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	relay, ok := c.relays[req.GetRelayId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "relay not found")
	}
	relay.LastHeartbeatUnixMs = req.GetTimestampUnixMs()
	return &registryv1.HeartbeatRelayResponse{}, nil
}

// ListRelays returns cloned in-memory Relay records in stable identifier order.
//
// Parameters:
//   - ctx: is accepted for interface compatibility; the in-memory operation completes synchronously.
//   - request: carries no filters in the current Registry contract.
//   - options: are accepted for gRPC client compatibility and are not used by the in-memory implementation.
//
// Returns:
//   - response: contains the sorted Relay snapshot.
//   - error: is always nil.
func (c *MemoryClient) ListRelays(context.Context, *registryv1.ListRelaysRequest, ...grpc.CallOption) (*registryv1.ListRelaysResponse, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	relayIDs := make([]string, 0, len(c.relays))
	for relayID := range c.relays {
		relayIDs = append(relayIDs, relayID)
	}
	sort.Strings(relayIDs)

	relays := make([]*registryv1.Relay, 0, len(relayIDs))
	for _, relayID := range relayIDs {
		relays = append(relays, cloneRelay(c.relays[relayID]))
	}

	return &registryv1.ListRelaysResponse{Relays: relays}, nil
}

// RegisterAgent validates and stores a cloned Agent and, when supplied, its
// Relay placement.
//
// Parameters:
//   - ctx: is accepted for interface compatibility; the in-memory operation completes synchronously.
//   - req: contains the validated request payload.
//   - options: are accepted for gRPC client compatibility and are not used by the in-memory implementation.
//
// Returns:
//   - result: is the *registryv1.RegisterAgentResponse value produced by RegisterAgent.
//   - error: reports a missing Agent payload or identifier.
func (c *MemoryClient) RegisterAgent(_ context.Context, req *registryv1.RegisterAgentRequest, _ ...grpc.CallOption) (*registryv1.RegisterAgentResponse, error) {
	agent := req.GetAgent()
	if agent == nil || agent.GetAgentId() == "" {
		return nil, status.Error(codes.InvalidArgument, "agent_id is required")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.agents[agent.GetAgentId()] = cloneAgent(agent)
	if req.GetRelayId() != "" {
		c.placements[agent.GetAgentId()] = &registryv1.AgentPlacement{
			AgentId:           agent.GetAgentId(),
			RelayId:           req.GetRelayId(),
			LastUpdatedUnixMs: agent.GetLastHeartbeatUnixMs(),
		}
	}
	return &registryv1.RegisterAgentResponse{}, nil
}

// HeartbeatAgent replaces the heartbeat timestamp of an existing in-memory Agent.
//
// Parameters:
//   - ctx: is accepted for interface compatibility; the in-memory operation completes synchronously.
//   - req: contains the validated request payload.
//   - options: are accepted for gRPC client compatibility and are not used by the in-memory implementation.
//
// Returns:
//   - result: is the *registryv1.HeartbeatAgentResponse value produced by HeartbeatAgent.
//   - error: reports an unknown Agent identifier.
func (c *MemoryClient) HeartbeatAgent(_ context.Context, req *registryv1.HeartbeatAgentRequest, _ ...grpc.CallOption) (*registryv1.HeartbeatAgentResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	agent, ok := c.agents[req.GetAgentId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "agent not found")
	}
	agent.LastHeartbeatUnixMs = req.GetTimestampUnixMs()
	return &registryv1.HeartbeatAgentResponse{}, nil
}

// ListAgents returns cloned in-memory Agent records in stable identifier order.
//
// Parameters:
//   - ctx: is accepted for interface compatibility; the in-memory operation completes synchronously.
//   - request: carries no filters in the current Registry contract.
//   - options: are accepted for gRPC client compatibility and are not used by the in-memory implementation.
//
// Returns:
//   - response: contains the sorted Agent snapshot.
//   - error: is always nil.
func (c *MemoryClient) ListAgents(context.Context, *registryv1.ListAgentsRequest, ...grpc.CallOption) (*registryv1.ListAgentsResponse, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	agentIDs := make([]string, 0, len(c.agents))
	for agentID := range c.agents {
		agentIDs = append(agentIDs, agentID)
	}
	sort.Strings(agentIDs)

	agents := make([]*registryv1.Agent, 0, len(agentIDs))
	for _, agentID := range agentIDs {
		agents = append(agents, cloneAgent(c.agents[agentID]))
	}

	return &registryv1.ListAgentsResponse{Agents: agents}, nil
}

// GetAgentPlacement returns an Agent's current in-process Relay placement.
//
// Parameters:
//   - ctx: is accepted for interface compatibility; the in-memory operation completes synchronously.
//   - req: contains the validated request payload.
//   - options: are accepted for gRPC client compatibility and are not used by the in-memory implementation.
//
// Returns:
//   - response: contains a clone of the current placement.
//   - error: reports an Agent without a stored placement.
func (c *MemoryClient) GetAgentPlacement(_ context.Context, req *registryv1.GetAgentPlacementRequest, _ ...grpc.CallOption) (*registryv1.GetAgentPlacementResponse, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	placement, ok := c.placements[req.GetAgentId()]
	if !ok {
		return nil, status.Error(codes.NotFound, "agent placement not found")
	}
	return &registryv1.GetAgentPlacementResponse{
		Placement: clonePlacement(placement),
	}, nil
}

func cloneRelay(relay *registryv1.Relay) *registryv1.Relay {
	return &registryv1.Relay{
		RelayId:             relay.GetRelayId(),
		Address:             relay.GetAddress(),
		GrpcPort:            relay.GetGrpcPort(),
		LastHeartbeatUnixMs: relay.GetLastHeartbeatUnixMs(),
	}
}

func cloneAgent(agent *registryv1.Agent) *registryv1.Agent {
	return &registryv1.Agent{
		AgentId:             agent.GetAgentId(),
		LastHeartbeatUnixMs: agent.GetLastHeartbeatUnixMs(),
	}
}

func clonePlacement(placement *registryv1.AgentPlacement) *registryv1.AgentPlacement {
	return &registryv1.AgentPlacement{
		AgentId:           placement.GetAgentId(),
		RelayId:           placement.GetRelayId(),
		LastUpdatedUnixMs: placement.GetLastUpdatedUnixMs(),
	}
}

func unixMillis(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	return t.UnixMilli()
}
