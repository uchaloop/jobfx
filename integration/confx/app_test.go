package integration_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/uchaloop/confmaker/v2"
	"github.com/uchaloop/confx"
	"github.com/uchaloop/job"
	"github.com/uchaloop/job/assignment"
	"github.com/uchaloop/jobfx"
	"github.com/uchaloop/secret/v2"
	"go.uber.org/fx"
)

var _ jobfx.Application = (*confx.App)(nil)

type config struct {
	Token secret.Secret `env:"TOKEN,required"`
}

func (c *config) SetDefaults() { c.Token = secret.New("hidden-default") }

// Process args are restored; these tests intentionally do not run in parallel.
func setArgs(t *testing.T, args ...string) {
	t.Helper()
	previous := os.Args
	os.Args = append([]string{"job"}, args...)
	t.Cleanup(func() { os.Args = previous })
}

func TestCLIIsLazy(t *testing.T) {
	for _, arg := range []string{"-help", "-describe=json", "-describe=markdown", "-describe=env", "-unknown", "-describe=xml"} {
		t.Run(arg, func(t *testing.T) {
			setArgs(t, arg)
			var calls int
			source := confx.New(
				confx.Provide[config]("app"),
				confx.Runtime(fx.Invoke(func() { t.Error("constructed Fx during CLI") })),
			).WithOptions(confmaker.WithDiagnosticHandler(func(confmaker.LoadReport) { calls++ }))
			var output bytes.Buffer
			isHandled, err := source.HandleCLI([]string{arg}, &output)
			isValid := arg != "-unknown" && arg != "-describe=xml"
			if isHandled != isValid || (err == nil) != isValid || strings.Contains(output.String(), "hidden-default") {
				t.Fatalf("handled=%v err=%v output=%s", isHandled, err, &output)
			}
			app := jobfx.New(source)
			result, err := app.Start(context.Background())
			if result.IsCLIHandled != isValid || (err == nil) != isValid || result.Job != nil || calls != 0 {
				t.Fatalf("result=%+v err=%v reports=%d", result, err, calls)
			}
			if err := app.Stop(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestRuntimeDiagnostics(t *testing.T) {
	setArgs(t)
	for _, isValid := range []bool{false, true} {
		env := map[string]string{"JOB_TIMEOUT": "1s"}
		if !isValid {
			env["JOB_TIMEOUT"] = "invalid"
		}
		var calls int
		var state confmaker.LoadState
		source := confx.New(
			confx.Provide[job.Config]("job"),
			confx.Runtime(
				fx.NopLogger,
				jobfx.Module(),
				fx.Provide(func() job.Func { return func(context.Context) (int64, error) { return 1, nil } }),
			),
		).WithOptions(
			confmaker.WithEnv(env),
			confmaker.WithDiagnosticHandler(func(report confmaker.LoadReport) {
				calls++

				state = report.State
			}),
		)
		app := jobfx.New(source)
		result, err := app.Start(context.Background())
		if (err == nil) != isValid || calls != 1 {
			t.Fatalf("result=%+v err=%v reports=%d", result, err, calls)
		}
		if isValid && (state != confmaker.LoadSucceeded || result.Job == nil) {
			t.Fatal("missing success")
		}
		if !isValid && (state != confmaker.LoadFailed || result.Job != nil) {
			t.Fatal("missing failure")
		}
		if err := app.Stop(); err != nil {
			t.Fatal(err)
		}
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestCLIWriter(t *testing.T) {
	for _, args := range [][]string{{"-help"}, {"-describe=json"}} {
		if isHandled, err := confx.New().HandleCLI(args, failWriter{}); isHandled || !errors.Is(err, io.ErrClosedPipe) {
			t.Fatalf("handled=%v err=%v", isHandled, err)
		}
		if isHandled, err := confx.New().HandleCLI(args, nil); !isHandled || err != nil {
			t.Fatalf("handled=%v err=%v", isHandled, err)
		}
	}
}

func TestExecuteConfxProcess(t *testing.T) {
	for _, arg := range []string{"run", "skip", "assignment-error", "-help", "-describe=json", "-unknown"} {
		t.Run(arg, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()

			cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestExecuteConfxHelper$")
			cmd.Env = append(os.Environ(), "JOBFX_CONFX_EXECUTE="+arg)
			output, err := cmd.CombinedOutput()
			if arg == "-unknown" || arg == "assignment-error" {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 || len(output) != 0 {
					t.Fatalf("err=%v output=%s", err, output)
				}

				return
			}

			if err != nil || strings.Contains(string(output), "hidden-default") {
				t.Fatalf("err=%v output=%s", err, output)
			}

			if arg == "skip" {
				if len(output) != 0 {
					t.Fatalf("skip output=%s", output)
				}

				return
			}

			if arg == "run" {
				if string(output) != "work\ncleanup\n" {
					t.Fatalf("output=%s", output)
				}

				return
			}

			if len(output) == 0 || strings.Contains(string(output), "work\n") || strings.Contains(string(output), "cleanup\n") {
				t.Fatalf("CLI constructed runtime or produced no output: %s", output)
			}
		})
	}
}

func TestExecuteConfxHelper(t *testing.T) {
	arg := os.Getenv("JOBFX_CONFX_EXECUTE")
	if len(arg) == 0 {
		return
	}

	os.Args = []string{"job"}
	if strings.HasPrefix(arg, "-") {
		os.Args = append(os.Args, arg)
	}

	jobfx.Execute(confx.New(
		confx.Provide[job.Config]("job"),
		confx.Provide[config]("app"),
		confx.Runtime(
			fx.NopLogger,
			jobfx.Module(),
			fx.Provide(func() job.Func {
				return func(context.Context) (int64, error) {
					fmt.Println("work")

					return 1, nil
				}
			}),
			fx.Invoke(func(lc fx.Lifecycle) {
				lc.Append(fx.Hook{OnStop: func(context.Context) error {
					fmt.Println("cleanup")

					return nil
				}})
			}),
		),
	).WithOptions(confmaker.WithEnv(map[string]string{"APP_TOKEN": "hidden-input"})),
		jobfx.WithAssignment(func(context.Context) (assignment.Config, assignment.Invocation, error) {
			if strings.HasPrefix(arg, "-") {
				panic("CLI called assignment source")
			}

			if arg == "assignment-error" {
				return assignment.Config{}, assignment.Invocation{}, errors.New("assignment source failed")
			}

			current := "a"
			if arg == "skip" {
				current = "b"
			}

			return assignment.Config{Clusters: []string{"a", "b"}, Current: current, Period: time.Minute},
				assignment.Invocation{ScheduledAt: time.Unix(0, 0)}, nil
		}),
	)
}
