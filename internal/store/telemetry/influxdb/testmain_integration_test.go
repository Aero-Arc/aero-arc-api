//go:build integration

package influxdb

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Aero-Arc/aero-arc-api/internal/testsupport"
)

type influxDBIntegrationConfig struct {
	host     string
	token    string
	database string
}

var integrationInfluxDB influxDBIntegrationConfig

func TestMain(m *testing.M) {
	os.Exit(runIntegrationSuite(m))
}

func runIntegrationSuite(m *testing.M) int {
	override := influxDBIntegrationConfig{
		host:     os.Getenv("AERO_API_TEST_INFLUXDB_HOST"),
		token:    os.Getenv("AERO_API_TEST_INFLUXDB_TOKEN"),
		database: os.Getenv("AERO_API_TEST_INFLUXDB_DATABASE"),
	}
	configured := 0
	for _, value := range []string{override.host, override.token, override.database} {
		if value != "" {
			configured++
		}
	}
	if configured > 0 && configured < 3 {
		_, _ = fmt.Fprintln(os.Stderr, "AERO_API_TEST_INFLUXDB_HOST, AERO_API_TEST_INFLUXDB_TOKEN, and AERO_API_TEST_INFLUXDB_DATABASE must be set together")
		return 1
	}
	if configured == 3 {
		integrationInfluxDB = override
		return m.Run()
	}

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	instance, err := testsupport.StartInfluxDB(ctx, os.Stderr)
	cancel()
	if err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "provision InfluxDB integration dependency: %v\n", err)
		return 1
	}
	integrationInfluxDB = influxDBIntegrationConfig{
		host:     instance.Host,
		token:    instance.Token,
		database: instance.Database,
	}
	code := m.Run()
	if err := instance.Dependency.Shutdown(code != 0, os.Stderr); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "clean up InfluxDB integration dependency: %v\n", err)
		code = 1
	}
	return code
}
