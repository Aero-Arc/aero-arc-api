package factory

import (
	"fmt"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	interussprovider "github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider/interuss"
	"github.com/Aero-Arc/aero-arc-api/internal/spatialindex"
	"github.com/Aero-Arc/aero-arc-api/internal/store/durable"
)

type Config struct {
	Providers []string
	InterUSS  interussprovider.Config
}

// New constructs the configured providers in their declared order.
func New(
	cfg Config,
	durableStore durable.Store,
	localIndex spatialindex.CandidateFinder,
) ([]airspaceprovider.Provider, error) {
	providers := make([]airspaceprovider.Provider, 0, len(cfg.Providers))
	for _, name := range cfg.Providers {
		switch name {
		case airspaceprovider.ProviderLocal:
			if localIndex == nil {
				return nil, fmt.Errorf("local airspace provider requires a spatial index")
			}
			providers = append(providers, airspaceprovider.NewLocalSpatialProvider(durableStore, localIndex))
		case airspaceprovider.ProviderInterUSS:
			provider, err := interussprovider.New(cfg.InterUSS)
			if err != nil {
				return nil, err
			}
			providers = append(providers, provider)
		default:
			return nil, fmt.Errorf("unsupported airspace provider %q", name)
		}
	}
	return providers, nil
}
