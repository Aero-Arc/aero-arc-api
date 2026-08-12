//go:build integration

package testsupport

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	InfluxDBImage    = "influxdb:3.10.3-core"
	InfluxDBDatabase = "aero_arc_test"
	// The client requires a non-empty token. The isolated test container runs
	// without authentication, so this value is configuration rather than a
	// credential.
	InfluxDBToken = "integration-test-token"
)

type InfluxDB struct {
	Host       string
	Token      string
	Database   string
	Dependency *Dependency
}

// StartInfluxDB provisions one in-memory InfluxDB 3 Core node and creates the
// database used by the importing integration package.
func StartInfluxDB(ctx context.Context, output io.Writer) (instance *InfluxDB, err error) {
	var dependency *Dependency
	defer func() {
		if recovered := recover(); recovered != nil {
			if dependency != nil {
				_ = dependency.Shutdown(true, output)
			}
			instance = nil
			err = fmt.Errorf("start %s: container provider panicked: %v", InfluxDBImage, recovered)
		}
	}()
	container, err := testcontainers.Run(ctx, InfluxDBImage,
		testcontainers.WithExposedPorts("8181/tcp"),
		testcontainers.WithCmd(
			"influxdb3", "serve",
			"--node-id=api-integration-test",
			"--object-store=memory",
			"--without-auth",
		),
		testcontainers.WithWaitStrategy(
			wait.ForHTTP("/health").
				WithPort("8181/tcp").
				WithPollInterval(250*time.Millisecond).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", InfluxDBImage, err)
	}
	dependency = newDependency(container, "InfluxDB", InfluxDBImage)
	fail := func(cause error) (*InfluxDB, error) {
		if cleanupErr := dependency.Shutdown(true, output); cleanupErr != nil {
			return nil, fmt.Errorf("%w; cleanup after setup failure: %v", cause, cleanupErr)
		}
		return nil, cause
	}
	host, err := container.Host(ctx)
	if err != nil {
		return fail(fmt.Errorf("resolve InfluxDB host: %w", err))
	}
	port, err := container.MappedPort(ctx, "8181/tcp")
	if err != nil {
		return fail(fmt.Errorf("resolve InfluxDB port: %w", err))
	}
	endpoint := (&url.URL{Scheme: "http", Host: net.JoinHostPort(host, port.Port())}).String()
	instance = &InfluxDB{
		Host:       endpoint,
		Token:      InfluxDBToken,
		Database:   InfluxDBDatabase,
		Dependency: dependency,
	}
	if err := createInfluxDBDatabase(ctx, instance); err != nil {
		return fail(err)
	}
	return instance, nil
}

func createInfluxDBDatabase(ctx context.Context, instance *InfluxDB) error {
	body, err := json.Marshal(map[string]string{"db": instance.Database})
	if err != nil {
		return fmt.Errorf("encode InfluxDB database request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, instance.Host+"/api/v3/configure/database", bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build InfluxDB database request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("create InfluxDB database %q: %w", instance.Database, err)
	}
	defer resp.Body.Close()
	responseBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("create InfluxDB database %q: status=%s body=%s", instance.Database, resp.Status, responseBody)
	}
	return nil
}
