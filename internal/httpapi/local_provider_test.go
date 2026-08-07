package httpapi

import (
	"testing"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	localprovider "github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider/local"
	"github.com/Aero-Arc/aero-arc-api/internal/service/deconfliction"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

func newTestLocalProvider(store durable.Store) airspaceprovider.Provider {
	return localprovider.New(store)
}

func newTestDeconflictionService(t *testing.T, store durable.Store) *deconfliction.DeconflictionService {
	t.Helper()
	service, err := deconfliction.NewDeconflictionService(store, newTestLocalProvider(store))
	if err != nil {
		t.Fatal(err)
	}
	return service
}
