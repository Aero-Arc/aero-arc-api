package httpapi

import (
	"net/http"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/service"
	"github.com/mrshabel/mach"
)

type Server struct {
	fleet          *service.FleetService
	requestTimeout time.Duration
}

func New(fleet *service.FleetService, requestTimeout time.Duration) *Server {
	return &Server{fleet: fleet, requestTimeout: requestTimeout}
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

	api.POST("/batteries", s.handleCreateBattery)
	api.POST("/maintenance-events", s.handleCreateMaintenanceEvent)

	return app
}
