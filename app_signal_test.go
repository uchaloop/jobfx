//go:build unix

package jobfx_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/uchaloop/job"
	"github.com/uchaloop/jobfx"
	"go.uber.org/fx"
)

func TestAppSignal(t *testing.T) {
	if os.Getenv("JOBFX_SIGNAL_TEST") == "1" {
		app := jobfx.MakeApp(
			fx.NopLogger,
			fx.Supply(job.Config{Timeout: time.Second}),
			provideWork(func(ctx context.Context) (int64, error) {
				if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
					return 0, err
				}
				<-ctx.Done()

				return 0, ctx.Err()
			}),
			jobfx.Module(),
		)
		result, err := app.Start(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if err := app.Stop(); err != nil {
			t.Fatal(err)
		}
		if result.Job.Outcome != job.OutcomeCanceled {
			t.Fatal(result.Job.Outcome)
		}
		fmt.Println("signal canceled work")

		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAppSignal$")
	cmd.Env = append(os.Environ(), "JOBFX_SIGNAL_TEST=1")
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s", err, output)
	}
}
