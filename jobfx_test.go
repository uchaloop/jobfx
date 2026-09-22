package jobfx_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxtest"

	"github.com/uchaloop/job"
	"github.com/uchaloop/jobfx"
)

func provideWork(fn job.Func) fx.Option {
	return fx.Provide(func() job.Func { return fn })
}

// The module hands over a Runner and nothing else happens to it.
func TestModule_ProvidesARunner(t *testing.T) {
	var runner *job.Runner

	app := fxtest.New(
		t,
		fx.Supply(job.Config{Timeout: time.Second}),
		provideWork(func(context.Context) (int64, error) { return 1, nil }),
		jobfx.Module(),
		fx.Populate(&runner),
	)

	app.RequireStart()
	app.RequireStop()

	if runner == nil {
		t.Fatal("no runner was provided")
	}
}

// The point of the split: starting the application must not start the work.
// A one-shot process runs it after Start returns, when its dependencies are
// ready; a scheduler runs it on its own schedule.
func TestModule_RunsNothingOnStart(t *testing.T) {
	var calls atomic.Int64

	var runner *job.Runner

	app := fxtest.New(
		t,
		fx.Supply(job.Config{Timeout: time.Second}),
		provideWork(func(context.Context) (int64, error) {
			calls.Add(1)

			return 0, nil
		}),
		jobfx.Module(),
		fx.Populate(&runner),
	)

	app.RequireStart()

	if n := calls.Load(); n != 0 {
		t.Fatalf("the work ran %d times during Start, want 0", n)
	}

	// The application decides when, and only then does it run.
	runner.Run(context.Background())

	if n := calls.Load(); n != 1 {
		t.Errorf("the work ran %d times after one Run, want 1", n)
	}

	app.RequireStop()
}

// Options passed to Module are applied first, so the container's can still
// override them - and the order inside one set is the order written.
func TestModule_OptionOrder(t *testing.T) {
	var order []string

	mark := func(name string) job.Middleware {
		return func(next job.Func) job.Func {
			return func(ctx context.Context) (int64, error) {
				order = append(order, name)

				return next(ctx)
			}
		}
	}

	var runner *job.Runner

	app := fxtest.New(
		t,
		fx.Supply(job.Config{Timeout: time.Second}),
		provideWork(func(context.Context) (int64, error) {
			order = append(order, "work")

			return 0, nil
		}),

		fx.Provide(func() jobfx.Options {
			return jobfx.Options{
				job.WithMiddleware(mark("container-first")),
				job.WithMiddleware(mark("container-second")),
			}
		}),

		jobfx.Module(job.WithMiddleware(mark("static"))),
		fx.Populate(&runner),
	)

	app.RequireStart()
	runner.Run(context.Background())
	app.RequireStop()

	want := []string{"static", "container-first", "container-second", "work"}
	if len(order) != len(want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
}

func TestModule_OptionsAreOptional(t *testing.T) {
	app := fxtest.New(
		t,
		fx.Supply(job.Config{}),
		provideWork(func(context.Context) (int64, error) { return 0, nil }),
		jobfx.Module(),
	)

	app.RequireStart()
	app.RequireStop()
}

// A configuration the Runner refuses fails the graph rather than surfacing
// later as a surprise at the first attempt.
func TestModule_RejectsABadConfig(t *testing.T) {
	app := fx.New(
		fx.Supply(job.Config{Timeout: -time.Second}),
		provideWork(func(context.Context) (int64, error) { return 0, nil }),
		jobfx.Module(),
		fx.Invoke(func(*job.Runner) {}),
		fx.NopLogger,
	)

	if app.Err() == nil {
		t.Fatal("the graph accepted a negative timeout")
	}
}
