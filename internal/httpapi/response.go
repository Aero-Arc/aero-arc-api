package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/Aero-Arc/aero-arc-api/internal/service"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
	"github.com/mrshabel/mach"
)

func writeJSON(c *mach.Context, statusCode int, v any) {
	c.Response.Header().Set("Content-Type", "application/json")
	c.Response.WriteHeader(statusCode)
	_ = json.NewEncoder(c.Response).Encode(v)
}

func writeError(c *mach.Context, statusCode int, message string) {
	writeJSON(c, statusCode, map[string]string{"error": message})
}

func writeServiceError(c *mach.Context, err error) {
	if errors.Is(err, durable.ErrNotFound) {
		writeError(c, http.StatusNotFound, "resource not found")
		return
	}
	if errors.Is(err, service.ErrValidation) {
		writeError(c, http.StatusBadRequest, strings.TrimSpace(err.Error()))
		return
	}
	if errors.Is(err, service.ErrInvalidTransition) || errors.Is(err, service.ErrActivationBlocked) {
		writeError(c, http.StatusConflict, strings.TrimSpace(err.Error()))
		return
	}
	if errors.Is(err, durable.ErrAlreadyExists) || errors.Is(err, durable.ErrVersionConflict) {
		writeError(c, http.StatusConflict, strings.TrimSpace(err.Error()))
		return
	}
	writeJSON(c, http.StatusInternalServerError, map[string]string{
		"error":   "request failed",
		"details": strings.TrimSpace(err.Error()),
	})
}

func writeCommandError(c *mach.Context, err error) {
	switch {
	case errors.Is(err, durable.ErrNotFound):
		writeJSON(c, http.StatusNotFound, map[string]string{"error": "AIRCRAFT_NOT_FOUND", "details": strings.TrimSpace(err.Error())})
	case errors.Is(err, service.ErrAircraftNotConnected):
		writeJSON(c, http.StatusConflict, map[string]string{"error": "AIRCRAFT_NOT_CONNECTED", "details": strings.TrimSpace(err.Error())})
	case errors.Is(err, context.DeadlineExceeded):
		writeJSON(c, http.StatusGatewayTimeout, map[string]string{"error": "COMMAND_TIMEOUT", "details": strings.TrimSpace(err.Error())})
	case errors.Is(err, service.ErrValidation):
		writeJSON(c, http.StatusBadRequest, map[string]string{"error": "INVALID_COMMAND", "details": strings.TrimSpace(err.Error())})
	case errors.Is(err, service.ErrAircraftCommandDelivery):
		writeJSON(c, http.StatusBadGateway, map[string]string{"error": "COMMAND_DELIVERY_FAILED", "details": strings.TrimSpace(err.Error())})
	default:
		writeJSON(c, http.StatusInternalServerError, map[string]string{"error": "COMMAND_FAILED", "details": strings.TrimSpace(err.Error())})
	}
}
