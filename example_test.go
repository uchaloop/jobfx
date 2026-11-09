package jobfx_test

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"go.uber.org/fx"

	"github.com/uchaloop/job"
	"github.com/uchaloop/job/assignment"
	"github.com/uchaloop/job/middleware/recovery"
	"github.com/uchaloop/jobfx"
)

// ExampleMakeApp shows lazy cluster selection before any Fx construction.
// App owns startup, work and observation; the caller defers cleanup and decides
// the process exit code. Work never runs inside an Fx hook.
func ExampleMakeApp() {
	fmt.Println("exit code:", runOnce())
	// Output: exit code: 0
}

func runOnce() int {
	// The launcher passes the scheduled point in - a Kubernetes CronJob through
	// its annotation, a Linux wrapper through an argument. A pod that starts
	// late must not take it from the clock, or it would decide as a later point
	// than the one it was meant to serve.
	scheduledAt := time.Date(2026, 9, 19, 4, 0, 0, 0, time.UTC)

	app := jobfx.MakeApp(
		fx.Supply(job.Config{Timeout: 4 * time.Minute}),
		fx.Supply(slog.Default()),

		fx.Provide(func() job.Func {
			return func(ctx context.Context) (int64, error) {
				// One bounded batch of application work.
				return 3, ctx.Err()
			}
		}),

		fx.Provide(func(log *slog.Logger) jobfx.Options {
			return jobfx.Options{
				job.WithMiddleware(recovery.MakeMiddleware(recovery.WithLogger(log))),
			}
		}),

		jobfx.Module(),
		fx.Provide(func(log *slog.Logger) job.Handler {
			return job.HandlerFunc(func(_ context.Context, result job.Result) {
				log.Info("attempt completed",
					"outcome", result.Outcome,
					"processed", result.Processed,
					"duration", result.Duration,
				)
			})
		}),
		fx.NopLogger,
	).WithAssignment(func(ctx context.Context) (assignment.Config, assignment.Invocation, error) {
		if err := ctx.Err(); err != nil {
			return assignment.Config{}, assignment.Invocation{}, err
		}

		return assignment.Config{
			Clusters: []string{"el", "xc", "dm"},
			Current:  "el",
			Period:   4 * time.Hour,
		}, assignment.Invocation{ScheduledAt: scheduledAt}, nil
	})

	defer func() {
		if err := app.Stop(); err != nil {
			slog.Error("stop dependencies", "error", err)
		}
	}()

	result, err := app.Start(context.Background())
	if err != nil {
		slog.Error("start application", "error", err)

		return 1
	}

	return result.ExitCode()
}
