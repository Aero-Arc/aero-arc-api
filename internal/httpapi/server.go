package httpapi

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/service"
	"github.com/Aero-Arc/aero-arc-api/internal/service/deconfliction"
	"github.com/mrshabel/mach"
)

type Server struct {
	fleet          *service.FleetService
	intents        *service.IntentService
	preflight      *service.PreflightService
	conformance    *service.ConformanceService
	deconfliction  *deconfliction.DeconflictionService
	requestTimeout time.Duration
	debug          bool
	ussAuthorizer  USSAuthorizer
}

// New constructs httpapi from the supplied configuration and dependencies.
//
// Parameters:
//   - fleet: is the *service.FleetService value supplied to New.
//   - requestTimeout: defines the time bound applied by the operation.
//
// Returns:
//   - result: is the *Server value produced by New.
func New(fleet *service.FleetService, requestTimeout time.Duration) *Server {
	return &Server{fleet: fleet, requestTimeout: requestTimeout}
}

// NewWithWorkflows constructs httpapi from the supplied configuration and dependencies.
//
// Parameters:
//   - fleet: is the *service.FleetService value supplied to NewWithWorkflows.
//   - intents: is the *service.IntentService value supplied to NewWithWorkflows.
//   - preflight: is the *service.PreflightService value supplied to NewWithWorkflows.
//   - conformance: is the *service.ConformanceService value supplied to NewWithWorkflows.
//   - requestTimeout: defines the time bound applied by the operation.
//   - deconflictionServices: is the ...*deconfliction.DeconflictionService value supplied to NewWithWorkflows.
//
// Returns:
//   - result: is the *Server value produced by NewWithWorkflows.
func NewWithWorkflows(fleet *service.FleetService, intents *service.IntentService, preflight *service.PreflightService, conformance *service.ConformanceService, requestTimeout time.Duration, deconflictionServices ...*deconfliction.DeconflictionService) *Server {
	server := &Server{
		fleet:          fleet,
		intents:        intents,
		preflight:      preflight,
		conformance:    conformance,
		requestTimeout: requestTimeout,
	}
	if len(deconflictionServices) > 0 {
		server.deconfliction = deconflictionServices[0]
	}
	return server
}

// WithDebug enables or disables debug-only HTTP behavior on the server.
//
// Parameters:
//   - debug: is the bool value supplied to WithDebug.
//
// Returns:
//   - result: is the *Server value produced by WithDebug.
func (s *Server) WithDebug(debug bool) *Server {
	s.debug = debug
	return s
}

// WithUSSAuthorizer installs the authorization policy used by USS routes.
//
// Parameters:
//   - authorizer: is the USSAuthorizer value supplied to WithUSSAuthorizer.
//
// Returns:
//   - result: is the *Server value produced by WithUSSAuthorizer.
func (s *Server) WithUSSAuthorizer(authorizer USSAuthorizer) *Server {
	s.ussAuthorizer = authorizer
	return s
}

// Handler returns the fully configured HTTP route handler.
//
// Returns:
//   - result: is the http.Handler value produced by Handler.
func (s *Server) Handler() http.Handler {
	app := mach.New()
	app.Use(mach.CORSWithConfig(mach.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodOptions},
		AllowHeaders: []string{"Content-Type", "Authorization"},
	}))
	if s.debug {
		app.Use(debugRequestLogger())
	}

	app.GET("/healthz", s.handleHealthz)
	app.GET("/readyz", s.handleReadyz)
	if s.deconfliction != nil && s.deconfliction.PublishingEnabled() {
		app.GET("/uss/v1/operational_intents/{entity_id}", s.handleGetUSSOperationalIntent)
		app.POST("/uss/v1/operational_intents", s.handleNotifyUSSOperationalIntentChanged)
	}

	api := app.Group("/api/v1")
	api.GET("/overview", s.handleGetOverviewDashboard)
	api.GET("/operations", s.handleGetOperationsDashboard)
	api.GET("/preflight", s.handleGetPreflightDashboard)
	api.GET("/conformance", s.handleGetConformanceDashboard)
	api.GET("/maintenance", s.handleGetMaintenanceDashboard)
	api.GET("/records", s.handleGetRecordsDashboard)

	api.GET("/aircraft", s.handleListAircraft)
	api.POST("/aircraft", s.handleCreateAircraft)
	api.GET("/aircraft/{aircraft_id}", s.handleGetAircraft)
	api.GET("/aircraft/{aircraft_id}/state", s.handleGetAircraftLiveState)
	api.GET("/aircraft/{aircraft_id}/map", s.handleGetAircraftMap)
	api.GET("/aircraft/{aircraft_id}/flights", s.handleListAircraftFlights)
	api.GET("/flights/{flight_id}", s.handleGetFlight)
	api.GET("/flights/{flight_id}/replay", s.handleGetFlightReplay)

	if s.workflowsAvailable() {
		api.POST("/operational-intents", s.handleCreateOperationalIntent)
		api.POST("/operational-intents/{intent_id}/modify", s.handleModifyOperationalIntent)
		api.POST("/operational-intents/{intent_id}/volumes", s.handleAddOperationalVolume)
		api.POST("/operational-intents/{intent_id}/submit", s.handleSubmitOperationalIntent)
		api.POST("/operational-intents/{intent_id}/preflight/evaluate", s.handleEvaluateOperationalIntentPreflight)
		if s.deconfliction != nil {
			api.POST("/operational-intents/{intent_id}/deconfliction/check", s.handleCheckOperationalIntentDeconfliction)
			api.GET("/operational-intents/{intent_id}/conflicts", s.handleListOperationalIntentConflicts)
			api.GET("/operational-intents/{intent_id}/coordination", s.handleGetOperationalIntentCoordination)
		}
		api.POST("/operational-intents/{intent_id}/accept", s.handleAcceptOperationalIntent)
		api.POST("/operational-intents/{intent_id}/activate", s.handleActivateOperationalIntent)
		api.POST("/operational-intents/{intent_id}/complete", s.handleCompleteOperationalIntent)
		api.POST("/operational-intents/{intent_id}/cancel", s.handleCancelOperationalIntent)
		api.GET("/operational-intents/{intent_id}/conformance", s.handleGetOperationalIntentConformance)
		api.POST("/telemetry", s.handleTelemetry)
	}

	api.POST("/batteries", s.handleCreateBattery)
	api.POST("/maintenance-events", s.handleCreateMaintenanceEvent)

	return app
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

// WriteHeader records the first HTTP status code written by the wrapped response writer.
//
// Parameters:
//   - status: is the int value supplied to WriteHeader.
func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func debugRequestLogger() mach.MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			recorder := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(recorder, r)
			slog.Debug("api operation",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.String("query", r.URL.RawQuery),
				slog.Int("status", recorder.status),
				slog.Duration("duration", time.Since(start)),
			)
		})
	}
}

func (s *Server) workflowsAvailable() bool {
	return s.intents != nil && s.preflight != nil && s.conformance != nil
}
