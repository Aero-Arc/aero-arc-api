package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/domain"
)

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleReadyz(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) handleListAircraft(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.contextWithTimeout(r)
	defer cancel()

	dashboards, err := s.fleet.ListAircraftDashboards(ctx)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"aircraft": dashboards})
}

func (s *Server) handleGetAircraft(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.contextWithTimeout(r)
	defer cancel()

	dashboard, err := s.fleet.GetAircraftDashboard(ctx, r.PathValue("aircraft_id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, dashboard)
}

func (s *Server) handleListAircraftFlights(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.contextWithTimeout(r)
	defer cancel()

	flights, err := s.fleet.ListFlightRecords(ctx, r.PathValue("aircraft_id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"flights": flights})
}

func (s *Server) handleGetFlight(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.contextWithTimeout(r)
	defer cancel()

	flight, err := s.fleet.GetFlightRecord(ctx, r.PathValue("flight_id"))
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, flight)
}

func (s *Server) handleGetFlightReplay(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := s.contextWithTimeout(r)
	defer cancel()

	limit, err := parseLimit(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	replay, err := s.fleet.GetFlightReplay(ctx, r.PathValue("flight_id"), limit)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, replay)
}

func (s *Server) handleCreateAircraft(w http.ResponseWriter, r *http.Request) {
	var aircraft domain.Aircraft
	if err := decodeJSON(r, &aircraft); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(aircraft.ID) == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	now := time.Now().UTC()
	if aircraft.CreatedAt.IsZero() {
		aircraft.CreatedAt = now
	}
	aircraft.UpdatedAt = now

	ctx, cancel := s.contextWithTimeout(r)
	defer cancel()
	if err := s.fleet.CreateAircraft(ctx, aircraft); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, aircraft)
}

func (s *Server) handleCreateBattery(w http.ResponseWriter, r *http.Request) {
	var battery domain.Battery
	if err := decodeJSON(r, &battery); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(battery.ID) == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	now := time.Now().UTC()
	if battery.CreatedAt.IsZero() {
		battery.CreatedAt = now
	}
	battery.UpdatedAt = now

	ctx, cancel := s.contextWithTimeout(r)
	defer cancel()
	if err := s.fleet.CreateBattery(ctx, battery); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, battery)
}

func (s *Server) handleCreateMaintenanceEvent(w http.ResponseWriter, r *http.Request) {
	var event domain.MaintenanceEvent
	if err := decodeJSON(r, &event); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	if strings.TrimSpace(event.ID) == "" {
		writeError(w, http.StatusBadRequest, "id is required")
		return
	}
	if strings.TrimSpace(event.AircraftID) == "" {
		writeError(w, http.StatusBadRequest, "aircraft_id is required")
		return
	}
	if event.OpenedAt.IsZero() {
		event.OpenedAt = time.Now().UTC()
	}

	ctx, cancel := s.contextWithTimeout(r)
	defer cancel()
	if err := s.fleet.RecordMaintenanceEvent(ctx, event); err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, event)
}

func (s *Server) contextWithTimeout(r *http.Request) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), s.requestTimeout)
}

func parseLimit(r *http.Request) (int, error) {
	raw := r.URL.Query().Get("limit")
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

func decodeJSON(r *http.Request, dst any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dst); err != nil {
		return err
	}
	return nil
}
