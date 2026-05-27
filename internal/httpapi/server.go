package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/registry"
	registryv1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/registry/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type Server struct {
	registry       *registry.Client
	requestTimeout time.Duration
}

type OverviewResponse struct {
	GeneratedAt            time.Time `json:"generated_at"`
	TotalRelays            int       `json:"total_relays"`
	HealthyRelays          int       `json:"healthy_relays"`
	TotalAgents            int       `json:"total_agents"`
	PlacedAgents           int       `json:"placed_agents"`
	UnplacedAgents         int       `json:"unplaced_agents"`
	UnknownPlacementAgents int       `json:"unknown_placement_agents"`
}

type RelayView struct {
	RelayID         string `json:"relay_id"`
	Address         string `json:"address"`
	GrpcPort        int32  `json:"grpc_port"`
	LastHeartbeatMS int64  `json:"last_heartbeat_unix_ms"`
}

type AgentView struct {
	AgentID         string `json:"agent_id"`
	LastHeartbeatMS int64  `json:"last_heartbeat_unix_ms"`
}

type PlacementView struct {
	AgentID       string `json:"agent_id"`
	RelayID       string `json:"relay_id"`
	LastUpdatedMS int64  `json:"last_updated_unix_ms"`
}

type PlacementsResponse struct {
	Placements []PlacementView `json:"placements"`
	NotFound   []string        `json:"not_found"`
}

func New(registryClient *registry.Client, requestTimeout time.Duration) *Server {
	return &Server{registry: registryClient, requestTimeout: requestTimeout}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/v1/overview", s.handleOverview)
	mux.HandleFunc("/v1/relays", s.handleRelays)
	mux.HandleFunc("/v1/agents", s.handleAgents)
	mux.HandleFunc("/v1/placements", s.handlePlacements)

	return withCORS(mux)
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleOverview(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.requestTimeout)
	defer cancel()

	relaysResp, err := s.registry.ListRelays(ctx)
	if err != nil {
		writeRegistryError(w, err)
		return
	}

	agentsResp, err := s.registry.ListAgents(ctx)
	if err != nil {
		writeRegistryError(w, err)
		return
	}

	placed, notFound, unknown := s.getPlacementStats(ctx, agentsResp.Agents)
	healthyRelays := countHealthyRelays(relaysResp.Relays)

	writeJSON(w, http.StatusOK, OverviewResponse{
		GeneratedAt:            time.Now().UTC(),
		TotalRelays:            len(relaysResp.Relays),
		HealthyRelays:          healthyRelays,
		TotalAgents:            len(agentsResp.Agents),
		PlacedAgents:           placed,
		UnplacedAgents:         notFound,
		UnknownPlacementAgents: unknown,
	})
}

func (s *Server) handleRelays(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.requestTimeout)
	defer cancel()

	resp, err := s.registry.ListRelays(ctx)
	if err != nil {
		writeRegistryError(w, err)
		return
	}

	relays := make([]RelayView, 0, len(resp.Relays))
	for _, relay := range resp.Relays {
		relays = append(relays, RelayView{
			RelayID:         relay.RelayId,
			Address:         relay.Address,
			GrpcPort:        relay.GrpcPort,
			LastHeartbeatMS: relay.LastHeartbeatUnixMs,
		})
	}

	sort.Slice(relays, func(i, j int) bool { return relays[i].RelayID < relays[j].RelayID })
	writeJSON(w, http.StatusOK, map[string]any{"relays": relays})
}

func (s *Server) handleAgents(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.requestTimeout)
	defer cancel()

	resp, err := s.registry.ListAgents(ctx)
	if err != nil {
		writeRegistryError(w, err)
		return
	}

	agents := make([]AgentView, 0, len(resp.Agents))
	for _, agent := range resp.Agents {
		agents = append(agents, AgentView{
			AgentID:         agent.AgentId,
			LastHeartbeatMS: agent.LastHeartbeatUnixMs,
		})
	}

	sort.Slice(agents, func(i, j int) bool { return agents[i].AgentID < agents[j].AgentID })
	writeJSON(w, http.StatusOK, map[string]any{"agents": agents})
}

func (s *Server) handlePlacements(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), s.requestTimeout)
	defer cancel()

	agentsResp, err := s.registry.ListAgents(ctx)
	if err != nil {
		writeRegistryError(w, err)
		return
	}

	placements := make([]PlacementView, 0, len(agentsResp.Agents))
	notFound := make([]string, 0)

	for _, agent := range agentsResp.Agents {
		placementResp, err := s.registry.GetAgentPlacement(ctx, agent.AgentId)
		if err != nil {
			if status.Code(err) == codes.NotFound {
				notFound = append(notFound, agent.AgentId)
				continue
			}
			writeRegistryError(w, err)
			return
		}

		p := placementResp.Placement
		placements = append(placements, PlacementView{
			AgentID:       p.AgentId,
			RelayID:       p.RelayId,
			LastUpdatedMS: p.LastUpdatedUnixMs,
		})
	}

	sort.Slice(placements, func(i, j int) bool { return placements[i].AgentID < placements[j].AgentID })
	sort.Strings(notFound)

	writeJSON(w, http.StatusOK, PlacementsResponse{Placements: placements, NotFound: notFound})
}

func (s *Server) getPlacementStats(ctx context.Context, agents []*registryv1.Agent) (placed int, notFound int, unknown int) {
	for _, agent := range agents {
		_, err := s.registry.GetAgentPlacement(ctx, agent.AgentId)
		if err == nil {
			placed++
			continue
		}

		switch status.Code(err) {
		case codes.NotFound:
			notFound++
		default:
			unknown++
		}
	}
	return
}

func countHealthyRelays(relays []*registryv1.Relay) int {
	now := time.Now().UnixMilli()
	const healthyWindowMS = int64(60_000)

	healthy := 0
	for _, relay := range relays {
		if now-relay.LastHeartbeatUnixMs <= healthyWindowMS {
			healthy++
		}
	}
	return healthy
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func writeRegistryError(w http.ResponseWriter, err error) {
	code := http.StatusBadGateway
	message := "registry request failed"

	if errors.Is(err, context.DeadlineExceeded) {
		code = http.StatusGatewayTimeout
		message = "registry request timed out"
	}

	s := status.Code(err)
	switch s {
	case codes.InvalidArgument:
		code = http.StatusBadRequest
		message = "invalid request"
	case codes.NotFound:
		code = http.StatusNotFound
		message = "resource not found"
	case codes.Unavailable:
		code = http.StatusServiceUnavailable
		message = "registry unavailable"
	case codes.DeadlineExceeded:
		code = http.StatusGatewayTimeout
		message = "registry request timed out"
	}

	writeJSON(w, code, map[string]string{
		"error":   message,
		"details": sanitizeError(err),
	})
}

func sanitizeError(err error) string {
	if err == nil {
		return ""
	}
	return strings.TrimSpace(err.Error())
}

func writeJSON(w http.ResponseWriter, statusCode int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(v)
}
