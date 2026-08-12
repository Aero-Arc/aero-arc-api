//go:build integration

package testsupport

import (
	"context"
	"fmt"
	"io"
	"time"

	"github.com/testcontainers/testcontainers-go"
)

const dependencyShutdownTimeout = 30 * time.Second

// Dependency owns a test-only container. Integration package TestMain
// functions keep one Dependency alive for the package and call Shutdown after
// m.Run so related tests share one service without hiding failure logs.
type Dependency struct {
	container testcontainers.Container
	name      string
	image     string
}

func newDependency(container testcontainers.Container, name, image string) *Dependency {
	return &Dependency{container: container, name: name, image: image}
}

// Shutdown writes container logs when the suite failed, then terminates the
// container. Externally managed services never create a Dependency.
func (d *Dependency) Shutdown(failed bool, output io.Writer) error {
	if d == nil || d.container == nil {
		return nil
	}
	if failed {
		logCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		logs, err := d.container.Logs(logCtx)
		if err != nil {
			_, _ = fmt.Fprintf(output, "%s container logs unavailable: %v\n", d.name, err)
		} else {
			_, _ = fmt.Fprintf(output, "--- %s container logs (%s) ---\n", d.name, d.shortID())
			_, _ = io.Copy(output, logs)
			_ = logs.Close()
			_, _ = fmt.Fprintln(output)
		}
		cancel()
	}

	ctx, cancel := context.WithTimeout(context.Background(), dependencyShutdownTimeout)
	defer cancel()
	if err := testcontainers.TerminateContainer(d.container, testcontainers.StopContext(ctx)); err != nil {
		return fmt.Errorf("terminate %s container %s (%s): %w", d.name, d.shortID(), d.image, err)
	}
	return nil
}

func (d *Dependency) shortID() string {
	id := d.container.GetContainerID()
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
