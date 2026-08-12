//go:build integration

package testsupport

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	PostGISImage    = "postgis/postgis:14-3.5-alpine"
	postGISDatabase = "aero_arc_test"
	postGISUser     = "aero_arc_test"
	postGISPassword = "aero_arc_test"
)

type PostGIS struct {
	URL        string
	Dependency *Dependency
}

// StartPostGIS provisions one isolated PostGIS database on a Docker-assigned
// host port. The image entrypoint creates the configured database before the
// readiness strategy completes.
func StartPostGIS(ctx context.Context, output io.Writer) (instance *PostGIS, err error) {
	var dependency *Dependency
	defer func() {
		if recovered := recover(); recovered != nil {
			if dependency != nil {
				_ = dependency.Shutdown(true, output)
			}
			instance = nil
			err = fmt.Errorf("start %s: container provider panicked: %v", PostGISImage, recovered)
		}
	}()
	container, err := testcontainers.Run(ctx, PostGISImage,
		testcontainers.WithExposedPorts("5432/tcp"),
		testcontainers.WithEnv(map[string]string{
			"POSTGRES_DB":       postGISDatabase,
			"POSTGRES_USER":     postGISUser,
			"POSTGRES_PASSWORD": postGISPassword,
		}),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").
				WithOccurrence(2).
				WithStartupTimeout(90*time.Second),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("start %s: %w", PostGISImage, err)
	}
	dependency = newDependency(container, "PostGIS", PostGISImage)
	fail := func(cause error) (*PostGIS, error) {
		if cleanupErr := dependency.Shutdown(true, output); cleanupErr != nil {
			return nil, fmt.Errorf("%w; cleanup after setup failure: %v", cause, cleanupErr)
		}
		return nil, cause
	}
	host, err := container.Host(ctx)
	if err != nil {
		return fail(fmt.Errorf("resolve PostGIS host: %w", err))
	}
	port, err := container.MappedPort(ctx, "5432/tcp")
	if err != nil {
		return fail(fmt.Errorf("resolve PostGIS port: %w", err))
	}
	databaseURL := (&url.URL{
		Scheme:   "postgres",
		User:     url.UserPassword(postGISUser, postGISPassword),
		Host:     net.JoinHostPort(host, port.Port()),
		Path:     postGISDatabase,
		RawQuery: "sslmode=disable",
	}).String()
	if err := awaitPostGIS(ctx, databaseURL); err != nil {
		return fail(err)
	}
	return &PostGIS{URL: databaseURL, Dependency: dependency}, nil
}

func awaitPostGIS(ctx context.Context, databaseURL string) error {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("configure PostGIS readiness connection: %w", err)
	}
	defer pool.Close()
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	var lastErr error
	for {
		pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		lastErr = pool.Ping(pingCtx)
		cancel()
		if lastErr == nil {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("await PostGIS readiness: %w (last ping: %v)", ctx.Err(), lastErr)
		case <-ticker.C:
		}
	}
}
