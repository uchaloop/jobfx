package integration_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/uchaloop/confmaker/v2"
	"github.com/uchaloop/confx"
	"github.com/uchaloop/job"
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
