package jobfx_test

import (
	"context"
	"errors"
	"io"
	"slices"
	"testing"
	"time"

	"github.com/uchaloop/job"
	"github.com/uchaloop/job/middleware/recovery"
	"github.com/uchaloop/jobfx"
	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
)

type cliApplication struct {
	isHandled bool
	err       error
	calls     int
}

func (a *cliApplication) HandleCLI([]string, io.Writer) (bool, error) {
	return a.isHandled, a.err
}

func (a *cliApplication) FX() fx.Option {
	a.calls++

	return fx.Error(errors.New("unexpected graph"))
}

func TestAppCLI(t *testing.T) {
	want := errors.New("cli failed")
	for _, test := range []struct {
		name      string
		isHandled bool
		err       error
	}{
		{name: "handled", isHandled: true},
		{name: "error", err: want},
	} {
		t.Run(test.name, func(t *testing.T) {
			source := &cliApplication{isHandled: test.isHandled, err: test.err}
			app := jobfx.New(source)
			result, err := app.Start(context.Background())
			if !errors.Is(err, test.err) || result.IsCLIHandled != test.isHandled || result.Job != nil || source.calls != 0 {
				t.Fatalf("result=%+v err=%v graph calls=%d", result, err, source.calls)
			}

			if err := app.Stop(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAppValidation(t *testing.T) {
	var nilSource *cliApplication
	for _, test := range []struct {
		name string
		app  *jobfx.App
		ctx  context.Context
		want error
	}{
		{
			name: "nil application",
			app:  jobfx.New(nil),
			ctx:  context.Background(),
			want: jobfx.ErrApplicationIsNil,
		},
		{
			name: "typed nil",
			app:  jobfx.New(nilSource),
			ctx:  context.Background(),
			want: jobfx.ErrApplicationIsNil,
		},
		{
			name: "zero app",
			app:  &jobfx.App{},
			ctx:  context.Background(),
			want: jobfx.ErrApplicationIsNil,
		},
		{name: "nil context", app: jobfx.MakeApp(), want: jobfx.ErrContextIsNil},
		{
			name: "startup",
			app:  jobfx.MakeApp().WithConfig(jobfx.AppConfig{StartupTimeout: -1}),
			ctx:  context.Background(),
			want: jobfx.ErrInvalidTimeout,
		},
		{
			name: "observe",
			app:  jobfx.MakeApp().WithConfig(jobfx.AppConfig{ObserveTimeout: -1}),
			ctx:  context.Background(),
			want: jobfx.ErrInvalidTimeout,
		},
		{
			name: "cleanup",
			app:  jobfx.MakeApp().WithConfig(jobfx.AppConfig{CleanupTimeout: -1}),
			ctx:  context.Background(),
			want: jobfx.ErrInvalidTimeout,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := test.app.Start(test.ctx)
			if !errors.Is(err, test.want) || result.Job != nil {
				t.Fatalf("result=%+v err=%v", result, err)
			}

			if err := test.app.Stop(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAppOrderAndCleanupError(t *testing.T) {
	var phases []string
	want := errors.New("cleanup failed")
	app := jobfx.MakeApp(
		fx.NopLogger,
		fx.Supply(job.Config{}),
		provideWork(func(context.Context) (int64, error) {
			phases = append(phases, "work")

			return 3, nil
		}),
		fx.Provide(func() job.Handler {
			return job.HandlerFunc(func(context.Context, job.Result) { phases = append(phases, "observe") })
		}),
		fx.Invoke(func(lc fx.Lifecycle) {
			phases = append(phases, "construct")
			lc.Append(fx.Hook{
				OnStart: func(context.Context) error {
					phases = append(phases, "start")

					return nil
				},
				OnStop: func(context.Context) error {
					phases = append(phases, "stop")

					return want
				},
			})
		}),
		jobfx.Module(),
	)
	if len(phases) != 0 {
		t.Fatal("eager construction")
	}

	result, err := app.Start(context.Background())
	if err != nil || result.ExitCode() != 0 || result.Job.Processed != 3 {
		t.Fatalf("result=%+v err=%v", result, err)
	}

	for range 2 {
		if err := app.Stop(); !errors.Is(err, want) {
			t.Fatal(err)
		}
	}

	if !slices.Equal(phases, []string{"construct", "start", "work", "observe", "stop"}) {
		t.Fatal(phases)
	}

	if _, err := app.Start(context.Background()); !errors.Is(err, jobfx.ErrAppUsed) {
		t.Fatal(err)
	}

	if _, err := app.WithConfig(jobfx.AppConfig{}).Start(context.Background()); !errors.Is(err, jobfx.ErrAppUsed) {
		t.Fatal(err)
	}
}

func TestAppBusyAndCanceledObservation(t *testing.T) {
	work := make(chan struct{})
	done := make(chan error, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	app := jobfx.MakeApp(
		fx.NopLogger,
		fx.Supply(job.Config{}),
		provideWork(func(ctx context.Context) (int64, error) {
			close(work)
			<-ctx.Done()

			return 0, ctx.Err()
		}),
		fx.Provide(func() job.Handler {
			return job.HandlerFunc(func(ctx context.Context, result job.Result) {
				if ctx.Err() != nil || result.Outcome != job.OutcomeCanceled {
					t.Errorf("observation ctx=%v result=%+v", ctx.Err(), result)
				}
			})
		}),
		jobfx.Module(),
	)
	go func() {
		_, err := app.Start(ctx)
		done <- err
	}()
	<-work

	if err := app.Stop(); !errors.Is(err, jobfx.ErrAppBusy) {
		t.Fatal(err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}

	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestAppRollback(t *testing.T) {
	want := errors.New("startup failed")
	var stops int
	app := jobfx.MakeApp(
		fx.NopLogger,
		fx.Supply(job.Config{}),
		provideWork(func(context.Context) (int64, error) {
			t.Error("work after failed start")

			return 0, nil
		}),
		jobfx.Module(),
		fx.Invoke(func(lc fx.Lifecycle) {
			lc.Append(fx.Hook{
				OnStop: func(context.Context) error {
					stops++

					return nil
				},
			})
			lc.Append(fx.Hook{OnStart: func(context.Context) error { return want }})
		}),
	)
	if result, err := app.Start(context.Background()); !errors.Is(err, want) || result.Job != nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}

	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}

	if stops != 1 {
		t.Fatalf("stops=%d", stops)
	}
}

func TestAppTimeoutAndPanic(t *testing.T) {
	t.Run("job timeout", func(t *testing.T) {
		app := jobfx.MakeApp(
			fx.NopLogger,
			fx.Supply(job.Config{Timeout: time.Millisecond}),
			provideWork(func(ctx context.Context) (int64, error) {
				<-ctx.Done()

				return 0, nil
			}),
			jobfx.Module(),
		)
		result, err := app.Start(context.Background())
		if err != nil || result.Job.Outcome != job.OutcomeTimeout || result.ExitCode() != 1 {
			t.Fatalf("result=%+v err=%v", result, err)
		}

		if err := app.Stop(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("panic cleanup", func(t *testing.T) {
		var isStopped bool
		app := jobfx.MakeApp(
			fx.NopLogger,
			fx.Supply(job.Config{}),
			provideWork(func(context.Context) (int64, error) { panic("work panic") }),
			jobfx.Module(),
			fx.Invoke(func(lc fx.Lifecycle) {
				lc.Append(fx.Hook{
					OnStop: func(context.Context) error {
						isStopped = true

						return nil
					},
				})
			}),
		)
		func() {
			defer func() {
				if recover() == nil {
					t.Error("panic was swallowed")
				}
			}()
			defer func() {
				if err := app.Stop(); err != nil {
					t.Error(err)
				}
			}()

			_, _ = app.Start(context.Background())
		}()
		if !isStopped {
			t.Fatal("cleanup skipped")
		}
	})
}

type eventLog struct{ isStopped bool }

func (l *eventLog) LogEvent(event fxevent.Event) {
	if _, isStop := event.(*fxevent.Stopping); isStop {
		l.isStopped = true
	}
}

func TestAppNoSyntheticTermination(t *testing.T) {
	var log eventLog
	app := jobfx.MakeApp(
		fx.WithLogger(func() fxevent.Logger { return &log }),
		fx.Supply(job.Config{}),
		provideWork(func(context.Context) (int64, error) { return 0, nil }),
		jobfx.Module(),
	)
	if _, err := app.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
	if log.isStopped {
		t.Fatal("synthetic termination event")
	}
}

func TestAppStopBeforeStart(t *testing.T) {
	app := jobfx.MakeApp()
	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := app.Start(context.Background()); !errors.Is(err, jobfx.ErrAppUsed) {
		t.Fatal(err)
	}
}

func TestAppBudgets(t *testing.T) {
	for _, cfg := range []jobfx.AppConfig{{}, {StartupTimeout: time.Second, ObserveTimeout: 2 * time.Second, CleanupTimeout: 3 * time.Second}} {
		startup, observe, cleanup := cfg.StartupTimeout, cfg.ObserveTimeout, cfg.CleanupTimeout
		if startup == 0 {
			startup, observe, cleanup = 30*time.Second, 10*time.Second, 30*time.Second
		}
		check := func(ctx context.Context, want time.Duration) {
			t.Helper()
			deadline, isSet := ctx.Deadline()
			left := time.Until(deadline)
			if !isSet || left <= 0 || left > want || left < want-time.Second/2 {
				t.Errorf("budget=%v want=%v", left, want)
			}
		}
		app := jobfx.MakeApp(
			fx.NopLogger,
			fx.Supply(job.Config{Timeout: time.Minute}),
			provideWork(func(ctx context.Context) (int64, error) {
				check(ctx, time.Minute)

				return 0, nil
			}),
			fx.Provide(func() job.Handler {
				return job.HandlerFunc(func(ctx context.Context, _ job.Result) { check(ctx, observe) })
			}),
			jobfx.Module(),
			fx.Invoke(func(lc fx.Lifecycle) {
				lc.Append(fx.Hook{
					OnStart: func(ctx context.Context) error {
						check(ctx, startup)

						return nil
					},
					OnStop: func(ctx context.Context) error {
						check(ctx, cleanup)

						return nil
					},
				})
			}),
		).WithConfig(cfg)
		if _, err := app.Start(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := app.Stop(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAppGraphErrorAndBusinessError(t *testing.T) {
	app := jobfx.MakeApp(fx.NopLogger)
	if result, err := app.Start(context.Background()); err == nil || result.Job != nil {
		t.Fatalf("%+v %v", result, err)
	}
	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}

	want := errors.New("work failed")
	app = jobfx.MakeApp(
		fx.NopLogger,
		fx.Supply(job.Config{}),
		provideWork(func(context.Context) (int64, error) { return 2, want }),
		jobfx.Module(),
	)
	result, err := app.Start(context.Background())
	if err != nil || !errors.Is(result.Job.Err, want) || result.ExitCode() != 1 {
		t.Fatalf("%+v %v", result, err)
	}
	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestResultExitCode(t *testing.T) {
	for _, test := range []struct {
		result jobfx.Result
		want   int
	}{
		{want: 1},
		{result: jobfx.Result{IsCLIHandled: true}},
		{result: jobfx.Result{Job: &job.Result{Outcome: job.OutcomeOK}}},
		{result: jobfx.Result{IsCLIHandled: true, Job: &job.Result{Outcome: job.OutcomeOK}}, want: 1},
		{result: jobfx.Result{Job: &job.Result{}}, want: 1},
	} {
		if code := test.result.ExitCode(); code != test.want {
			t.Fatalf("code=%d want=%d", code, test.want)
		}
	}
}

func TestAppStartupTimeout(t *testing.T) {
	app := jobfx.MakeApp(
		fx.NopLogger,
		fx.Supply(job.Config{}),
		provideWork(func(context.Context) (int64, error) {
			t.Error("work after timeout")

			return 0, nil
		}),
		jobfx.Module(),
		fx.Invoke(func(lc fx.Lifecycle) {
			lc.Append(fx.Hook{OnStart: func(ctx context.Context) error {
				<-ctx.Done()

				return ctx.Err()
			}})
		}),
	).WithConfig(jobfx.AppConfig{StartupTimeout: time.Millisecond})
	if result, err := app.Start(context.Background()); !errors.Is(err, context.DeadlineExceeded) || result.Job != nil {
		t.Fatalf("result=%+v err=%v", result, err)
	}

	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestAppRecoveryMiddleware(t *testing.T) {
	app := jobfx.MakeApp(
		fx.NopLogger,
		fx.Supply(job.Config{}),
		provideWork(func(context.Context) (int64, error) { panic("work panic") }),
		jobfx.Module(job.WithMiddleware(recovery.MakeMiddleware())),
	)
	result, err := app.Start(context.Background())
	if err != nil || result.Job.Outcome != job.OutcomePanic || result.ExitCode() != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestAppHandlerPanicCleanup(t *testing.T) {
	var isStopped bool
	app := jobfx.MakeApp(
		fx.NopLogger,
		fx.Supply(job.Config{}),
		provideWork(func(context.Context) (int64, error) { return 0, nil }),
		fx.Provide(func() job.Handler {
			return job.HandlerFunc(func(context.Context, job.Result) { panic("handler panic") })
		}),
		jobfx.Module(),
		fx.Invoke(func(lc fx.Lifecycle) {
			lc.Append(fx.Hook{OnStop: func(context.Context) error {
				isStopped = true

				return nil
			}})
		}),
	)
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic swallowed")
			}
		}()
		defer func() {
			if err := app.Stop(); err != nil {
				t.Error(err)
			}
		}()

		_, _ = app.Start(context.Background())
	}()
	if !isStopped {
		t.Fatal("cleanup skipped")
	}
}
