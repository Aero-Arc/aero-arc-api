package service

import (
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	deconflictionservice "github.com/Aero-Arc/aero-arc-api/internal/service/deconfliction"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

// DeconflictionService is retained as a compatibility alias while callers
// migrate to the dedicated service/deconfliction package.
type DeconflictionService = deconflictionservice.DeconflictionService

// Deprecated: import internal/service/deconfliction directly.
func NewDeconflictionService(store durable.Store, providers ...airspaceprovider.Provider) *DeconflictionService {
	return deconflictionservice.NewDeconflictionService(store, providers...)
}

// Deprecated: import internal/service/deconfliction directly.
func NewDeconflictionServiceWithClock(store durable.Store, now func() time.Time, providers ...airspaceprovider.Provider) *DeconflictionService {
	return deconflictionservice.NewDeconflictionServiceWithClock(store, now, providers...)
}
