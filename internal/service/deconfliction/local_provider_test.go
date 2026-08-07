package deconfliction_test

import (
	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	localprovider "github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider/local"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

func newTestLocalProvider(store durable.Store) airspaceprovider.Provider {
	return localprovider.New(store)
}
