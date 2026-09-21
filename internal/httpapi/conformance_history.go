package httpapi

import (
	"errors"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/service"
	conformancev1 "github.com/aero-arc/aero-arc-protos/gen/go/aeroarc/conformance/v1"
	"github.com/mrshabel/mach"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Server) handleGetConformanceHistory(c *mach.Context) {
	q := c.Request.URL.Query()
	request := &conformancev1.ListConformanceEventsRequest{PageToken: q.Get("page_token")}
	invalid := func() { writeError(c, http.StatusBadRequest, "invalid conformance history query") }
	if len(request.PageToken) > 4096 {
		invalid()
		return
	}
	if raw := q.Get("page_size"); raw != "" {
		v, err := strconv.Atoi(raw)
		if err != nil || v < 1 || v > 200 {
			invalid()
			return
		}
		request.PageSize = int32(v)
	}
	if raw := q.Get("generation"); raw != "" {
		v, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || v > math.MaxInt64 {
			invalid()
			return
		}
		request.AssignmentGeneration = v
	}
	for _, bound := range []struct {
		name string
		dst  **timestamppb.Timestamp
	}{{"from", &request.From}, {"until", &request.Until}} {
		if raw := q.Get(bound.name); raw != "" {
			v, err := time.Parse(time.RFC3339Nano, raw)
			if err != nil {
				invalid()
				return
			}
			stamp := timestamppb.New(v)
			if stamp.CheckValid() != nil {
				invalid()
				return
			}
			*bound.dst = stamp
		}
	}
	if request.From != nil && request.Until != nil && !request.From.AsTime().Before(request.Until.AsTime()) {
		invalid()
		return
	}
	ctx, cancel := s.contextWithTimeout(c)
	defer cancel()
	result, err := s.fleet.GetConformanceHistory(ctx, c.Param("intent_id"), request)
	if errors.Is(err, service.ErrConformanceHistoryUnavailable) {
		writeError(c, http.StatusServiceUnavailable, err.Error())
		return
	}
	if err != nil {
		writeServiceError(c, err)
		return
	}
	writeJSON(c, http.StatusOK, result)
}
