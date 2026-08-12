//go:build integration

package postgres

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/testsupport"
)

var integrationDatabaseURL string

func TestMain(m *testing.M) {
	os.Exit(runIntegrationSuite(m))
}

func runIntegrationSuite(m *testing.M) int {
	if databaseURL := os.Getenv("AERO_API_TEST_POSTGIS_URL"); databaseURL != "" {
		integrationDatabaseURL = databaseURL
		return m.Run()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	instance, err := testsupport.StartPostGIS(ctx, os.Stderr)
	cancel()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "provision PostGIS integration dependency: %v\n", err)
		return 1
	}
	integrationDatabaseURL = instance.URL
	code := m.Run()
	if err := instance.Dependency.Shutdown(code != 0, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "clean up PostGIS integration dependency: %v\n", err)
		code = 1
	}
	return code
}
