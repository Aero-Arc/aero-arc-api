package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
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

func (s *Server) handleGetAircraftLiveState(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()

	state, err := s.fleet.GetAircraftLiveState(ctx, c.Param("aircraft_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, state)
}

func (s *Server) handleGetAircraftMap(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()

	limit, err := parseLimit(c)
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	view, err := s.fleet.GetAircraftMapView(ctx, c.Param("aircraft_id"), limit)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, view)
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

func (s *Server) handleImportMission(c *mach.Context) {
	if !s.missionDeploymentControlEnabled() {
		writeError(c, http.StatusServiceUnavailable, "secure mission control is not configured")
		return
	}
	if !s.authorizeMissionDeployment(c) {
		writeError(c, http.StatusUnauthorized, "valid mission control authorization is required")
		return
	}
	const maxMissionImportBody = 2 << 20
	c.Request.Body = http.MaxBytesReader(c.Response, c.Request.Body, maxMissionImportBody)
	var req service.ImportMissionRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, http.StatusBadRequest, fmt.Sprintf("invalid mission import body: %v", err))
		return
	}
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	result, err := s.fleet.ImportMission(ctx, c.Param("flight_id"), c.Request.Header.Get("Idempotency-Key"), req)
	if err != nil {
		var validationErr service.MissionValidationError
		if errors.As(err, &validationErr) {
			writeJSON(c, http.StatusBadRequest, map[string]any{
				"error":               strings.TrimSpace(err.Error()),
				"validation_findings": validationErr.Findings,
			})
			return
		}
		writeServiceError(c, err)
		return
	}
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
		c.Response.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(c, status, result)
}

func (s *Server) handleGetCurrentMission(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	mission, err := s.fleet.GetCurrentMission(ctx, c.Param("flight_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, mission)
}

func (s *Server) handleDeployCurrentMission(c *mach.Context) {
	if !s.missionDeploymentControlEnabled() {
		writeError(c, http.StatusServiceUnavailable, "secure Relay mission control is not configured")
		return
	}
	if !s.authorizeMissionDeployment(c) {
		writeError(c, http.StatusUnauthorized, "valid mission deployment authorization is required")
		return
	}
	defer func() { _ = c.Request.Body.Close() }()
	var probe [1]byte
	if count, err := c.Request.Body.Read(probe[:]); count != 0 || (err != nil && !errors.Is(err, io.EOF)) {
		writeError(c, http.StatusBadRequest, "mission deployment request must have an empty body")
		return
	}
	ctx, cancel := context.WithTimeout(c.Context(), s.missionDeploymentTimeout)
	defer cancel()
	expectedDigest, err := parseMissionIfMatch(c.Request.Header.Get("If-Match"))
	if err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	result, err := s.fleet.DeployCurrentMission(ctx, c.Param("flight_id"), c.Param("mission_id"), expectedDigest, c.Request.Header.Get("Idempotency-Key"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	status := http.StatusOK
	if result.Deployment.Status == domain.MissionDeploymentPending || result.Deployment.Status == domain.MissionDeploymentTemporaryError || result.Deployment.Status == domain.MissionDeploymentOutcomeUnknown {
		status = http.StatusAccepted
	}
	if result.Replayed {
		c.Response.Header().Set("Idempotent-Replayed", "true")
	}
	writeJSON(c, status, result)
}

func parseMissionIfMatch(value string) (string, error) {
	value = strings.TrimSpace(value)
	if len(value) != 66 || value[0] != '"' || value[len(value)-1] != '"' {
		return "", errors.New(`If-Match must contain one quoted lowercase SHA-256 mission digest`)
	}
	digest := value[1 : len(value)-1]
	for _, character := range digest {
		if (character < '0' || character > '9') && (character < 'a' || character > 'f') {
			return "", errors.New(`If-Match must contain one quoted lowercase SHA-256 mission digest`)
		}
	}
	return digest, nil
}

func (s *Server) handleGetMissionDeployment(c *mach.Context) {
	if !s.missionDeploymentControlEnabled() {
		writeError(c, http.StatusServiceUnavailable, "secure Relay mission control is not configured")
		return
	}
	if !s.authorizeMissionDeployment(c) {
		writeError(c, http.StatusUnauthorized, "valid mission deployment authorization is required")
		return
	}
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	deployment, err := s.fleet.GetMissionDeployment(ctx, c.Param("flight_id"), c.Param("deployment_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, deployment)
}

func (s *Server) handleGetCurrentMissionDeployment(c *mach.Context) {
	if !s.missionDeploymentControlEnabled() {
		writeError(c, http.StatusServiceUnavailable, "secure Relay mission control is not configured")
		return
	}
	if !s.authorizeMissionDeployment(c) {
		writeError(c, http.StatusUnauthorized, "valid mission deployment authorization is required")
		return
	}
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	deployment, err := s.fleet.GetCurrentMissionDeployment(ctx, c.Param("flight_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, deployment)
}

func (s *Server) handleReconcileMissionDeployment(c *mach.Context) {
	if !s.missionDeploymentControlEnabled() {
		writeError(c, http.StatusServiceUnavailable, "secure Relay mission control is not configured")
		return
	}
	if !s.authorizeMissionDeployment(c) {
		writeError(c, http.StatusUnauthorized, "valid mission deployment authorization is required")
		return
	}
	defer func() { _ = c.Request.Body.Close() }()
	var probe [1]byte
	if count, err := c.Request.Body.Read(probe[:]); count != 0 || (err != nil && !errors.Is(err, io.EOF)) {
		writeError(c, http.StatusBadRequest, "mission deployment reconciliation request must have an empty body")
		return
	}
	ctx, cancel := context.WithTimeout(c.Context(), s.missionDeploymentTimeout)
	defer cancel()
	result, err := s.fleet.ReconcileMissionDeployment(ctx, c.Param("flight_id"), c.Param("deployment_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	status := http.StatusOK
	if missionDeploymentPending(result.Deployment.Status) {
		status = http.StatusAccepted
	}
	c.Response.Header().Set("Idempotent-Replayed", "true")
	writeJSON(c, status, result)
}

func missionDeploymentPending(status domain.MissionDeploymentStatus) bool {
	return status == domain.MissionDeploymentPending || status == domain.MissionDeploymentTemporaryError || status == domain.MissionDeploymentOutcomeUnknown
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

func (s *Server) handleInstallBattery(c *mach.Context) {
	var req service.InstallBatteryRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	installation, err := s.fleet.InstallBattery(ctx, c.Param("aircraft_id"), req)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusCreated, installation)
}

func (s *Server) handleCreatePlannedFlight(c *mach.Context) {
	var req service.CreateFlightRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	flight, err := s.fleet.CreatePlannedFlight(ctx, c.Param("intent_id"), req)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusCreated, flight)
}

func (s *Server) handleStartFlight(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	flight, err := s.fleet.StartFlight(ctx, c.Param("flight_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, flight)
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
	s.debugOperation(ctx, "create_intent", slog.String("aircraft_id", req.AircraftID), slog.String("requested_intent_id", req.ID))
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
	s.debugOperation(ctx, "add_operational_volume", slog.String("intent_id", c.Param("intent_id")), slog.String("requested_volume_id", req.ID))
	volume, err := s.intents.AddOperationalVolume(ctx, c.Param("intent_id"), req)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusCreated, volume)
}

func (s *Server) handleModifyOperationalIntent(c *mach.Context) {
	var req service.ModifyIntentRequest
	if err := decodeJSON(c, &req); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}

	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	s.debugOperation(ctx, "modify_intent", slog.String("intent_id", c.Param("intent_id")), slog.Int("expected_version", req.ExpectedVersion), slog.String("reason", req.Reason))
	result, err := s.intents.ModifyIntent(ctx, c.Param("intent_id"), req)
	if err != nil {
		var activeErr service.ActiveIntentModificationError
		if errors.As(err, &activeErr) {
			writeJSON(c, http.StatusConflict, map[string]any{
				"error":     "intent modification blocked",
				"code":      "active_intent_modification_blocked",
				"message":   fmt.Sprintf("Intent %s is active. End or supersede the active plan before modifying it.", activeErr.IntentID),
				"intent_id": activeErr.IntentID,
				"status":    activeErr.Status,
				"version":   activeErr.Version,
			})
			return
		}
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, result)
}

func (s *Server) handleSubmitOperationalIntent(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	s.debugOperation(ctx, "submit_intent", slog.String("intent_id", c.Param("intent_id")))
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
	s.debugOperation(ctx, "evaluate_preflight", slog.String("intent_id", c.Param("intent_id")))
	evaluation, err := s.preflight.EvaluateIntent(ctx, c.Param("intent_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, evaluation)
}

func (s *Server) handleCheckOperationalIntentDeconfliction(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	s.debugOperation(ctx, "check_deconfliction", slog.String("intent_id", c.Param("intent_id")))
	result, err := s.deconfliction.CheckIntent(ctx, c.Param("intent_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, result)
}

func (s *Server) handleListOperationalIntentConflicts(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	s.debugOperation(ctx, "list_conflicts", slog.String("intent_id", c.Param("intent_id")))
	findings, err := s.deconfliction.ListConflictFindings(ctx, c.Param("intent_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, map[string]any{"findings": findings})
}

func (s *Server) handleGetOperationalIntentCoordination(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	publication, err := s.deconfliction.GetPublication(ctx, c.Param("intent_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, publication)
}

func (s *Server) handleAcceptOperationalIntent(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	s.debugOperation(ctx, "accept_intent", slog.String("intent_id", c.Param("intent_id")))
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
	s.debugOperation(ctx, "activate_intent", slog.String("intent_id", c.Param("intent_id")))
	intent, err := s.intents.GetIntent(ctx, c.Param("intent_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if intent.Status != domain.IntentStatusAccepted {
		writeServiceError(c, fmt.Errorf("%w: %s -> %s", service.ErrInvalidTransition, intent.Status, domain.IntentStatusActive))
		return
	}
	evaluation, err := s.preflight.EvaluateIntent(ctx, c.Param("intent_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	if evaluation.Blocked {
		writeServiceError(c, fmt.Errorf("%w: preflight evaluation blocked", service.ErrActivationBlocked))
		return
	}
	intent, err = s.intents.ActivateIntent(ctx, c.Param("intent_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, intent)
}

func (s *Server) handleCompleteOperationalIntent(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	s.debugOperation(ctx, "complete_intent", slog.String("intent_id", c.Param("intent_id")))
	intent, err := s.intents.CompleteIntent(ctx, c.Param("intent_id"))
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, intent)
}

func (s *Server) handleCancelOperationalIntent(c *mach.Context) {
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	s.debugOperation(ctx, "cancel_intent", slog.String("intent_id", c.Param("intent_id")))
	intent, err := s.intents.CancelIntent(ctx, c.Param("intent_id"))
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
	s.debugOperation(ctx, "ingest_telemetry", slog.String("aircraft_id", sample.AircraftID), slog.String("intent_id", sample.IntentID), slog.String("sample_id", sample.ID))
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
	s.debugOperation(ctx, "get_intent_conformance", slog.String("intent_id", c.Param("intent_id")))
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

func (s *Server) debugOperation(ctx context.Context, operation string, attrs ...slog.Attr) {
	if !s.debug {
		return
	}
	attrs = append([]slog.Attr{slog.String("operation", operation)}, attrs...)
	slog.LogAttrs(ctx, slog.LevelDebug, "workflow operation", attrs...)
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
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return errors.New("request body must contain exactly one JSON object")
		}
		return err
	}
	return nil
}
