package httpapi

import (
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
