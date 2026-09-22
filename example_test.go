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

// ExampleModule shows the sequence a one-shot process owns: decide, start the
// dependencies, run once, observe, clean up, exit. None of it belongs in an Fx
// hook - running the work inside OnStart would fold its duration into the
// startup timeout, and asking fx.Shutdowner to finish would report an ordinary
// completion as a termination signal.
func ExampleModule() {
	fmt.Println("exit code:", runOnce())
	// Output: exit code: 0
}

func runOnce() int {
	// The launcher passes the scheduled point in - a Kubernetes CronJob through
	// its annotation, a Linux wrapper through an argument. A pod that starts
	// late must not take it from the clock, or it would decide as a later point
	// than the one it was meant to serve.
	scheduledFor := time.Date(2026, 9, 19, 4, 0, 0, 0, time.UTC)

	rotation, err := assignment.MakeRotation(assignment.Config{
		Clusters: []string{"el", "xc", "dm"},
		Current:  "el",
		Period:   4 * time.Hour,
	})
	if err != nil {
		slog.Error("configure rotation", "error", err)

		return 1
	}

	decision, err := rotation.Decide(assignment.Invocation{ScheduledFor: scheduledFor})
	if err != nil {
		slog.Error("decide owner", "error", err)

		return 1
	}

	if !decision.Execute {
		// Another cluster owns this point. Nothing ran, and that is a clean
		// finish - the decision itself is what observability records, because
		// no Result will ever be produced for it.
		slog.Info("point belongs elsewhere", "slot", decision.Slot, "owner", decision.Owner)

		return 0
	}

	var runner *job.Runner

	app := fx.New(
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
				job.WithMiddleware(recovery.Middleware(recovery.WithLogger(log))),
			}
		}),

		jobfx.Module(),
		fx.Populate(&runner),
		fx.NopLogger,
	)

	// Fx owns signal reception. Subscribe before startup so shutdown also
	// cancels startup hooks. The bridge lives until observation and cleanup end.
	shutdown := app.Wait()
	workCtx, cancelWork := context.WithCancel(context.Background())
	bridgeDone := make(chan struct{})
	bridgeStopped := make(chan struct{})
	go func() {
		defer close(bridgeStopped)
		select {
		case <-shutdown:
			cancelWork()
		case <-bridgeDone:
		}
	}()
	defer func() {
		close(bridgeDone)
		cancelWork()
		<-bridgeStopped
	}()

	// Dependencies first: no work before they are ready, and a failed start
	// rolls back whatever came up.
	startupCtx, cancelStartup := context.WithTimeout(workCtx, 30*time.Second)
	defer cancelStartup()

	if err := app.Start(startupCtx); err != nil {
		slog.Error("start dependencies", "error", err)

		return 1
	}

	result := runner.Run(workCtx)

	// The observation gets a bounded context of its own, because the work
	// context may already be cancelled.
	observeCtx, cancelObserve := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancelObserve()

	job.HandlerFunc(func(_ context.Context, r job.Result) {
		slog.Info("attempt completed",
			"outcome", r.Outcome, "processed", r.Processed, "duration", r.Duration)
	}).Handle(observeCtx, result)

	// Cleanup gets its own budget too, not the cancelled work context, so
	// exporters and pools still have time to finish.
	cleanupCtx, cancelCleanup := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancelCleanup()

	cleanupErr := app.Stop(cleanupCtx)
	if cleanupErr != nil {
		// Reported, but it does not decide the code: see below.
		slog.Error("stop dependencies", "error", cleanupErr)
	}

	// The code tells the scheduler whether to run this work again, so only the
	// attempt decides it. A failed flush after successful work would otherwise
	// repeat the business effect to fix an observability problem.
	if result.Outcome != job.OutcomeOK {
		return 1
	}

	return 0
}
