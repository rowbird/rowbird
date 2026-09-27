package api

import (
	"context"
	"log/slog"
	"time"

	"github.com/rowbird/rowbird/internal/api/gen"
	"github.com/rowbird/rowbird/internal/store"
)

// StoreHealth is what the readiness probe needs from the store.
type StoreHealth interface {
	Ping(ctx context.Context) error
	MigrationStatus(ctx context.Context) (store.MigrationStatus, error)
}

// SchedulerHealth says whether the scheduler of this instance is ticking.
type SchedulerHealth interface {
	Healthy() bool
}

const componentCheckTimeout = 2 * time.Second

type healthHandlers struct {
	store     StoreHealth
	scheduler SchedulerHealth
	logger    *slog.Logger
}

func (h *healthHandlers) GetHealthLive(context.Context, gen.GetHealthLiveRequestObject) (gen.GetHealthLiveResponseObject, error) {
	return gen.GetHealthLive200JSONResponse{Status: gen.LivenessStatusOk}, nil
}

// GetHealthReady reports each component. Error messages are fixed strings: the underlying errors
// may name hosts or users, so they are logged instead of returned.
func (h *healthHandlers) GetHealthReady(ctx context.Context, _ gen.GetHealthReadyRequestObject) (gen.GetHealthReadyResponseObject, error) {
	components := map[string]gen.ComponentStatus{}
	healthy := true
	fail := func(name, msg string, err error) {
		healthy = false
		m := msg
		components[name] = gen.ComponentStatus{Status: gen.ComponentStatusStatusError, Message: &m}
		if err != nil {
			h.logger.WarnContext(ctx, "readiness check failed", "component", name, "error", err)
		}
	}
	ok := gen.ComponentStatus{Status: gen.ComponentStatusStatusOk}

	checkCtx, cancel := context.WithTimeout(ctx, componentCheckTimeout)
	defer cancel()
	if err := h.store.Ping(checkCtx); err != nil {
		fail("store", "store is unreachable", err)
		fail("migrations", "unknown while the store is unreachable", nil)
	} else {
		components["store"] = ok
		status, err := h.store.MigrationStatus(checkCtx)
		switch {
		case err != nil:
			fail("migrations", "could not read the schema version", err)
		case !status.UpToDate():
			fail("migrations", "migrations are pending", nil)
		default:
			components["migrations"] = ok
		}
	}

	// Instances started without a scheduler do not report one.
	if h.scheduler != nil {
		if h.scheduler.Healthy() {
			components["scheduler"] = ok
		} else {
			fail("scheduler", "the scheduler has not completed a tick recently", nil)
		}
	}

	if !healthy {
		return gen.GetHealthReady503JSONResponse{Status: gen.ReadinessStatusUnavailable, Components: components}, nil
	}
	return gen.GetHealthReady200JSONResponse{Status: gen.ReadinessStatusOk, Components: components}, nil
}
