package jobfx

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/uchaloop/job"
	"go.uber.org/fx"
)

type executeApplication struct {
	isHandled bool
	err       error
}

func (a *executeApplication) HandleCLI([]string, io.Writer) (bool, error) {
	return a.isHandled, a.err
}

func (*executeApplication) FX() fx.Option {
	panic("CLI constructed Fx")
}

func TestExecuteInvalidInputs(t *testing.T) {
	var app *App
	var application *executeApplication
	var option *nilExecuteOption
	for _, inputs := range [][]any{nil, {nil}, {app}, {application}, {option}, {42}, {fx.NopLogger, New(application)}, {[]fx.Option{fx.NopLogger}}} {
		if code := execute(inputs...); code != 1 {
			t.Fatalf("code=%d", code)
		}
	}
}

type nilExecuteOption struct{ fx.Option }

func TestExecuteCLI(t *testing.T) {
	if code := execute(&executeApplication{isHandled: true}); code != 0 {
		t.Fatal(code)
	}

	if code := execute(&executeApplication{err: errors.New("CLI failed")}); code != 1 {
		t.Fatal(code)
	}
}

func TestExecuteProcess(t *testing.T) {
	for _, test := range []struct {
		mode string
		code int
	}{
		{mode: "options"},
		{mode: "configured"},
		{mode: "cleanup-error"},
		{mode: "business-error", code: 1},
		{mode: "startup-error", code: 1},
		{mode: "panic", code: 2},
	} {
		t.Run(test.mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExecuteProcessHelper$")
			cmd.Env = append(os.Environ(), "JOBFX_EXECUTE_TEST="+test.mode)
			output, err := cmd.CombinedOutput()
			code := 0
			if err != nil {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) {
					t.Fatal(err)
				}
				code = exitErr.ExitCode()
			}

			if code != test.code || !strings.Contains(string(output), "cleanup-complete") {
				t.Fatalf("code=%d want=%d output=%s", code, test.code, output)
			}

			if strings.Contains(string(output), "caller-defer") && test.mode != "panic" {
				t.Fatalf("Execute returned: %s", output)
			}

			if test.mode != "panic" && string(output) != "cleanup-complete\n" {
				t.Fatalf("unexpected helper logging: %s", output)
			}
		})
	}
}

func TestExecuteProcessHelper(t *testing.T) {
	mode := os.Getenv("JOBFX_EXECUTE_TEST")
	if len(mode) == 0 {
		return
	}

	defer fmt.Println("caller-defer")
	options := []fx.Option{
		fx.NopLogger,
		fx.Supply(job.Config{}),
		fx.Provide(func() job.Func {
			return func(context.Context) (int64, error) {
				if mode == "panic" {
					panic("work panic")
				}

				if mode == "business-error" {
					return 0, errors.New("work failed")
				}

				return 1, nil
			}
		}),
		Module(),
		fx.Invoke(func(lc fx.Lifecycle) {
			lc.Append(fx.Hook{OnStop: func(ctx context.Context) error {
				if mode == "configured" {
					deadline, isSet := ctx.Deadline()
					if !isSet || time.Until(deadline) > time.Second {
						panic("custom cleanup budget lost")
					}
				}
				fmt.Println("cleanup-complete")
				if mode == "cleanup-error" {
					return errors.New("cleanup failed")
				}

				return nil
			}})
			if mode == "startup-error" {
				lc.Append(fx.Hook{OnStart: func(context.Context) error { return errors.New("startup failed") }})
			}
		}),
	}
	if mode == "configured" {
		Execute(MakeApp(options...).WithConfig(AppConfig{CleanupTimeout: time.Second}))

		return
	}

	Execute(fx.Options(options...))
}

// A named nil slice can implement Application just like a nil pointer.
type nilSliceApplication []string

func (nilSliceApplication) HandleCLI([]string, io.Writer) (bool, error) {
	panic("nil application reached CLI")
}

func (nilSliceApplication) FX() fx.Option {
	panic("nil application reached Fx")
}

func TestNilApplicationRejectedByBothEntries(t *testing.T) {
	var pointer *executeApplication
	var slice nilSliceApplication
	for _, application := range []Application{nil, pointer, slice} {
		app := New(application)
		result, err := app.Start(context.Background())
		if !errors.Is(err, ErrApplicationIsNil) || result.Job != nil {
			t.Fatalf("result=%+v err=%v", result, err)
		}

		if err := app.Stop(); err != nil {
			t.Fatal(err)
		}

		if code := execute(application); code != 1 {
			t.Fatalf("exit code=%d", code)
		}
	}
}
