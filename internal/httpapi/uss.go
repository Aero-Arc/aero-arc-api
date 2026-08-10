package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Aero-Arc/dss-clients/interuss"
	"github.com/Aero-Arc/dss-clients/interuss/gen/scdv1"
	"github.com/mrshabel/mach"

	interussprovider "github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider/interuss"
	"github.com/Aero-Arc/aero-arc-api/internal/domain"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

func (s *Server) authorizeUSS(c *mach.Context) (string, bool) {
	if s.ussAuthorizer == nil {
		writeError(c, http.StatusServiceUnavailable, "USS authentication is not configured")
		return "", false
	}
	subject, err := s.ussAuthorizer.Authorize(c.Request, ussStrategicCoordinationScope)
	if err != nil {
		if errors.Is(err, errUSSForbidden) {
			writeError(c, http.StatusForbidden, "USS token does not grant strategic coordination")
			return "", false
		}
		writeError(c, http.StatusUnauthorized, "invalid USS bearer token")
		return "", false
	}
	return subject, true
}

func (s *Server) handleGetUSSOperationalIntent(c *mach.Context) {
	if _, ok := s.authorizeUSS(c); !ok {
		return
	}
	entityID := strings.TrimSpace(c.Param("entity_id"))
	if _, err := interuss.SCDEntityID(entityID); err != nil {
		writeError(c, http.StatusBadRequest, "entity_id must be a UUIDv4")
		return
	}
	version := 0
	if raw := c.Query("version"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed <= 0 {
			writeError(c, http.StatusBadRequest, "version must be a positive integer")
			return
		}
		version = parsed
	}
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	publication, volumes, err := s.deconfliction.GetPublishedOperationalIntent(ctx, entityID, version)
	if errors.Is(err, durable.ErrNotFound) {
		writeError(c, http.StatusNotFound, "operational intent is not published")
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	intent, err := interussprovider.PublishedOperationalIntent(publication.ReferenceJSON, volumes)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, scdv1.GetOperationalIntentDetailsResponse{OperationalIntent: intent})
}

func (s *Server) handleNotifyUSSOperationalIntentChanged(c *mach.Context) {
	subject, ok := s.authorizeUSS(c)
	if !ok {
		return
	}
	var notification scdv1.PutOperationalIntentDetailsParameters
	if err := decodeJSON(c, &notification); err != nil {
		writeError(c, http.StatusBadRequest, err.Error())
		return
	}
	entityID, err := notification.OperationalIntentId.AsEntityID()
	if err != nil {
		writeError(c, http.StatusBadRequest, "operational_intent_id must be a UUIDv4")
		return
	}
	entityUUID, err := entityID.AsUUIDv4Format()
	if err != nil {
		writeError(c, http.StatusBadRequest, "operational_intent_id must be a UUIDv4")
		return
	}
	intentID := entityUUID.String()
	received := domain.ReceivedPeerNotification{IntentID: intentID, Manager: subject, Deleted: true}
	if notification.OperationalIntent != nil {
		intent, err := notification.OperationalIntent.AsOperationalIntent()
		if err != nil {
			writeError(c, http.StatusBadRequest, "invalid operational intent notification")
			return
		}
		if intent.Reference.Manager != subject {
			writeError(c, http.StatusForbidden, "token subject does not match operational intent manager")
			return
		}
		nestedID, err := intent.Reference.Id.AsUUIDv4Format()
		if err != nil || nestedID.String() != intentID {
			writeError(c, http.StatusBadRequest, "operational intent reference ID does not match operational_intent_id")
			return
		}
		if intent.Reference.Ovn == nil {
			writeError(c, http.StatusBadRequest, "operational intent notification must include an OVN")
			return
		}
		ovn, err := intent.Reference.Ovn.AsEntityOVN()
		if err != nil || strings.TrimSpace(ovn) == "" {
			writeError(c, http.StatusBadRequest, "operational intent notification must include an OVN")
			return
		}
		received.IntentVersion = int(intent.Reference.Version)
		received.OVN = ovn
		received.Deleted = false
	}
	payload, err := json.Marshal(notification)
	if err != nil {
		writeServiceError(c, err)
		return
	}
	digest := sha256.Sum256(append([]byte(subject+"\x00"), payload...))
	received.ID = hex.EncodeToString(digest[:])
	received.Payload = payload
	received.ReceivedAt = time.Now().UTC()
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	if err := s.deconfliction.RecordReceivedPeerNotification(ctx, received); err != nil {
		writeServiceError(c, err)
		return
	}
	c.Response.WriteHeader(http.StatusNoContent)
}
