package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/service"
	"github.com/mrshabel/mach"
)

func (s *Server) handleHealthz(c *mach.Context) {
	writeJSON(c, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReadyz(c *mach.Context) {
	writeJSON(c, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleGetOverviewDashboard(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()

	dashboard, err := s.fleet.GetOverviewDashboard(ctx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, dashboard)
}

func (s *Server) handleGetOperationsDashboard(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()

	dashboard, err := s.fleet.GetOperationsDashboard(ctx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, dashboard)
}

func (s *Server) handleGetPreflightDashboard(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()

	dashboard, err := s.fleet.GetPreflightDashboard(ctx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, dashboard)
}

func (s *Server) handleGetConformanceDashboard(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()

	dashboard, err := s.fleet.GetConformanceDashboard(ctx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, dashboard)
}

func (s *Server) handleGetMaintenanceDashboard(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()

	dashboard, err := s.fleet.GetMaintenanceDashboard(ctx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, dashboard)
}

func (s *Server) handleGetRecordsDashboard(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()

	dashboard, err := s.fleet.GetRecordsDashboard(ctx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, dashboard)
}

func (s *Server) handleListAircraft(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()

	dashboards, err := s.fleet.ListAircraftDashboards(ctx)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"aircraft": dashboards})
}

func (s *Server) handleGetAircraft(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()

	dashboard, err := s.fleet.GetAircraftDashboard(ctx, c.Param("aircraft_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, dashboard)
}

func (s *Server) handleListAircraftFlights(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()

	flights, err := s.fleet.ListFlightRecords(ctx, c.Param("aircraft_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"flights": flights})
}

func (s *Server) handleGetFlight(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()

	flight, err := s.fleet.GetFlightRecord(ctx, c.Param("flight_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, flight)
}

func (s *Server) handleGetFlightReplay(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()

	limit, err := parseLimit(c)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	replay, err := s.fleet.GetFlightReplay(ctx, c.Param("flight_id"), limit)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, replay)
}

func (s *Server) handleCreateAircraft(c *mach.Context) {
	var aircraft domain.Aircraft
	if err := decodeJSON(c, &aircraft); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(aircraft.ID) == "" {
		writeError(c, http.StatusBadRequest, "id is required")
		return
	}
	now := time.Now().UTC()
	if aircraft.CreatedAt.IsZero() {
		aircraft.CreatedAt = now
	}
	aircraft.UpdatedAt = now

	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	if err := s.fleet.CreateAircraft(ctx, aircraft); err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusCreated, aircraft)
}

func (s *Server) handleCreateBattery(c *mach.Context) {
	var battery domain.Battery
	if err := decodeJSON(c, &battery); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(battery.ID) == "" {
		writeError(c, http.StatusBadRequest, "id is required")
		return
	}
	now := time.Now().UTC()
	if battery.CreatedAt.IsZero() {
		battery.CreatedAt = now
	}
	battery.UpdatedAt = now

	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	if err := s.fleet.CreateBattery(ctx, battery); err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusCreated, battery)
}

func (s *Server) handleCreateMaintenanceEvent(c *mach.Context) {
	var event domain.MaintenanceEvent
	if err := decodeJSON(c, &event); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(event.ID) == "" {
		writeError(c, http.StatusBadRequest, "id is required")
		return
	}
	if strings.TrimSpace(event.AircraftID) == "" {
		writeError(c, http.StatusBadRequest, "aircraft_id is required")
		return
	}
	if event.OpenedAt.IsZero() {
		event.OpenedAt = time.Now().UTC()
	}

	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	if err := s.fleet.RecordMaintenanceEvent(ctx, event); err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusCreated, event)
}

func (s *Server) handleCreateOperationalIntent(c *mach.Context) {
	var req service.CreateIntentRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	intent, err := s.intents.CreateIntent(ctx, req)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusCreated, intent)
}

func (s *Server) handleAddOperationalVolume(c *mach.Context) {
	var req service.AddOperationalVolumeRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	volume, err := s.intents.AddOperationalVolume(ctx, c.Param("intent_id"), req)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusCreated, volume)
}

func (s *Server) handleSubmitOperationalIntent(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	intent, err := s.intents.SubmitIntent(ctx, c.Param("intent_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, intent)
}

func (s *Server) handleEvaluateOperationalIntentPreflight(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	evaluation, err := s.preflight.EvaluateIntent(ctx, c.Param("intent_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, evaluation)
}

func (s *Server) handleAcceptOperationalIntent(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	intent, err := s.intents.AcceptIntent(ctx, c.Param("intent_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, intent)
}

func (s *Server) handleActivateOperationalIntent(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	evaluation, err := s.preflight.EvaluateIntent(ctx, c.Param("intent_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if evaluation.Blocked {
		writeServiceError(c, fmt.Errorf("%w: preflight evaluation blocked", service.ErrActivationBlocked))
		return
	}
	intent, err := s.intents.ActivateIntent(ctx, c.Param("intent_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, intent)
}

func (s *Server) handleTelemetry(c *mach.Context) {
	var sample domain.TelemetrySample
	if err := decodeJSON(c, &sample); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(sample.AircraftID) == "" {
		writeError(c, http.StatusBadRequest, "aircraft_id is required")
		return
	}

	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	evaluation, err := s.conformance.EvaluateTelemetry(ctx, sample)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, evaluation)
}

func (s *Server) handleGetOperationalIntentConformance(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	evaluation, err := s.conformance.GetIntentConformance(ctx, c.Param("intent_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, evaluation)
}

func (s *Server) contextWithTimeout(c *mach.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.Context(), s.requestTimeout)
}

func parseLimit(c *mach.Context) (int, error) {
	raw := c.Query("limit")
	if raw == "" {
		return 500, nil
	}
	limit, err := strconv.Atoi(raw)
	if err != nil || limit < 0 {
		return 0, errors.New("limit must be a non-negative integer")
	}
	if limit > 5000 {
		return 0, errors.New("limit must be <= 5000")
	}
	return limit, nil
}

func decodeJSON(c *mach.Context, dst any) error {
	defer c.Request.Body.Close()
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	return nil
}
