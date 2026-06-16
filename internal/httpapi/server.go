package httpapi

import (
	"net/http"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/service"
	"github.com/mrshabel/mach"
)

type Server struct {
	fleet          *service.FleetService
	intents        *service.IntentService
	preflight      *service.PreflightService
	conformance    *service.ConformanceService
	requestTimeout time.Duration
}

func New(fleet *service.FleetService, requestTimeout time.Duration) *Server {
	return &Server{fleet: fleet, requestTimeout: requestTimeout}
}

func NewWithWorkflows(fleet *service.FleetService, intents *service.IntentService, preflight *service.PreflightService, conformance *service.ConformanceService, requestTimeout time.Duration) *Server {
	return &Server{
		fleet:          fleet,
		intents:        intents,
		preflight:      preflight,
		conformance:    conformance,
		requestTimeout: requestTimeout,
	}
}

func (s *Server) Handler() http.Handler {
	app := mach.New()
	app.Use(mach.CORSWithConfig(mach.CORSConfig{
		AllowOrigins: []string{"*"},
		AllowMethods: []string{http.MethodGet, http.MethodPost, http.MethodOptions},
		AllowHeaders: []string{"Content-Type", "Authorization"},
	}))

	app.GET("/healthz", s.handleHealthz)
	app.GET("/readyz", s.handleReadyz)

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
	api.GET("/aircraft/{aircraft_id}/flights", s.handleListAircraftFlights)
	api.GET("/flights/{flight_id}", s.handleGetFlight)
	api.GET("/flights/{flight_id}/replay", s.handleGetFlightReplay)

	if s.workflowsAvailable() {
		api.POST("/operational-intents", s.handleCreateOperationalIntent)
		api.POST("/operational-intents/{intent_id}/volumes", s.handleAddOperationalVolume)
		api.POST("/operational-intents/{intent_id}/submit", s.handleSubmitOperationalIntent)
		api.POST("/operational-intents/{intent_id}/preflight/evaluate", s.handleEvaluateOperationalIntentPreflight)
		api.POST("/operational-intents/{intent_id}/accept", s.handleAcceptOperationalIntent)
		api.POST("/operational-intents/{intent_id}/activate", s.handleActivateOperationalIntent)
		api.GET("/operational-intents/{intent_id}/conformance", s.handleGetOperationalIntentConformance)
		api.POST("/telemetry", s.handleTelemetry)
	}

	api.POST("/batteries", s.handleCreateBattery)
	api.POST("/maintenance-events", s.handleCreateMaintenanceEvent)

	return app
}

func (s *Server) workflowsAvailable() bool {
	return s.intents != nil && s.preflight != nil && s.conformance != nil
}
