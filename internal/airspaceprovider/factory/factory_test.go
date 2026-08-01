package factory

import (
	"context"
	"strings"
	"testing"

	"github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider"
	interussprovider "github.com/Aero-Arc/aero-arc-api/internal/airspaceprovider/interuss"
	"github.com/Aero-Arc/aero-arc-api/internal/spatialindex"
	spatialmemory "github.com/Aero-Arc/aero-arc-api/internal/spatialindex/memory"
	durablememory "github.com/Aero-Arc/aero-arc-api/internal/store/durable/memory"
)

func TestNewUsesExplicitProviderOrder(t *testing.T) {
	projection := spatialindex.NewProjection(spatialmemory.New())
	if err := projection.Rebuild(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	defer projection.Close()

	providers, err := New(Config{
		Providers: []string{airspaceprovider.ProviderLocal, airspaceprovider.ProviderInterUSS},
		InterUSS: interussprovider.Config{
			BaseURL: "http://dss.example",
		},
	}, durablememory.NewStore(), projection)
	if err != nil {
		t.Fatal(err)
	}
	if len(providers) != 2 || providers[0].ID() != "local" || providers[1].ID() != "interuss_scd" {
		t.Fatalf("providers = %#v", providers)
	}
}

func TestNewRequiresIndexForLocalProvider(t *testing.T) {
	_, err := New(Config{
		Providers: []string{airspaceprovider.ProviderLocal},
	}, durablememory.NewStore(), nil)
	if err == nil || !strings.Contains(err.Error(), "requires a spatial index") {
		t.Fatalf("error = %v", err)
	}
}

func TestNewRejectsUnsupportedProvider(t *testing.T) {
	_, err := New(Config{
		Providers: []string{"unknown"},
	}, durablememory.NewStore(), nil)
	if err == nil || !strings.Contains(err.Error(), "unsupported airspace provider") {
		t.Fatalf("error = %v", err)
	}
}
