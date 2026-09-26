package httpapi

import (
	"net/http"

	"github.com/Aero-Arc/aero-arc-api/internal/service"
	"github.com/mrshabel/mach"
)

func (s *Server) commandAccess(c *mach.Context) bool {
	if !s.missionDeploymentControlEnabled() {
		writeError(c, http.StatusServiceUnavailable, "command control is not configured")
		return false
	}
	if !s.authorizeMissionDeployment(c) {
		writeError(c, http.StatusUnauthorized, "valid command authorization is required")
		return false
	}
	return true
}
func (s *Server) handleSubmitCommand(c *mach.Context) {
	if !s.commandAccess(c) {
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Response, c.Request.Body, 4096)
	var request service.CommandRequest
	if err := decodeJSON(c, &request); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	command, err := s.fleet.SubmitCommand(ctx, c.Param("flight_id"), "mission-control-service", c.Request.Header.Get("Idempotency-Key"), request)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	c.Response.Header().Set("Location", "/api/v1/flights/"+command.FlightID+"/commands/"+command.ID)
	writeJSON(c, http.StatusAccepted, command)
}
func (s *Server) handleListCommands(c *mach.Context) {
	if !s.commandAccess(c) {
		return
	}
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	commands, err := s.fleet.ListFlightCommands(ctx, c.Param("flight_id"), "mission-control-service")
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"commands": commands})
}

func (s *Server) handleGetCommand(c *mach.Context) {
	if !s.commandAccess(c) {
		return
	}
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	command, err := s.fleet.GetFlightCommand(ctx, c.Param("flight_id"), c.Param("command_id"), "mission-control-service")
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, command)
}

func (s *Server) handleReconcileCommand(c *mach.Context) {
	if !s.commandAccess(c) {
		return
	}
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	command, err := s.fleet.ReconcileFlightCommand(ctx, c.Param("flight_id"), c.Param("command_id"), "mission-control-service")
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusAccepted, command)
}

func (s *Server) handleGetFlightCompletion(c *mach.Context) {
	if !s.commandAccess(c) {
		return
	}
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	completion, err := s.fleet.GetFlightCompletion(ctx, c.Param("flight_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, completion)
}
