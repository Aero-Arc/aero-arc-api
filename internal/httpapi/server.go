package httpapi

import (
	"net/http"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/service"
)

type Server struct {
	fleet          *service.FleetService
	requestTimeout time.Duration
}

func New(fleet *service.FleetService, requestTimeout time.Duration) *Server {
	return &Server{fleet: fleet, requestTimeout: requestTimeout}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	mux.HandleFunc("GET /readyz", s.handleReadyz)

	mux.HandleFunc("GET /api/v1/aircraft", s.handleListAircraft)
	mux.HandleFunc("POST /api/v1/aircraft", s.handleCreateAircraft)
	mux.HandleFunc("GET /api/v1/aircraft/{aircraft_id}", s.handleGetAircraft)
	mux.HandleFunc("GET /api/v1/aircraft/{aircraft_id}/flights", s.handleListAircraftFlights)
	mux.HandleFunc("GET /api/v1/flights/{flight_id}", s.handleGetFlight)
	mux.HandleFunc("GET /api/v1/flights/{flight_id}/replay", s.handleGetFlightReplay)

	mux.HandleFunc("POST /api/v1/batteries", s.handleCreateBattery)
	mux.HandleFunc("POST /api/v1/maintenance-events", s.handleCreateMaintenanceEvent)

	return withCORS(mux)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}
