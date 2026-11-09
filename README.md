# jobfx

[![Go Reference](https://pkg.go.dev/badge/github.com/uchaloop/jobfx.svg)](https://pkg.go.dev/github.com/uchaloop/jobfx) [![CI](https://github.com/uchaloop/jobfx/actions/workflows/ci.yml/badge.svg)](https://github.com/uchaloop/jobfx/actions/workflows/ci.yml) [![Release](https://img.shields.io/github/v/tag/uchaloop/jobfx?label=release)](https://github.com/uchaloop/jobfx/tags) [![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Optional Uber Fx integration for [job](https://github.com/uchaloop/job).**
Job owns work execution; jobfx supplies Fx wiring and the optional one-shot App.
Importing job alone does not require Fx.

[Install](#installation) · [Quick start](#quick-start) · [Lifecycle](#run-the-process) · [Confx support](#optional-confx-support) · [Reference](#reference)

## Installation

Requires Go 1.27 or later. This README describes the current branch.
The command below installs the published release. The new App helpers require
this checkout until their release; use the local setup below to try them.

```sh
go get github.com/uchaloop/jobfx@latest
```

## Quick start

For a one-shot process, save this complete program as `main.go` in a separate
example directory. This example uses ordinary Fx options without confx.
It needs no environment variables or external services:

```go
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/uchaloop/job"
	"github.com/uchaloop/jobfx"
	"go.uber.org/fx"
)

func main() {
	os.Exit(run())
}

func run() int {
	app := jobfx.MakeApp(
		fx.Supply(job.Config{Timeout: 4 * time.Minute}),
		fx.Provide(func() job.Func {
			return func(ctx context.Context) (int64, error) {
				return 3, ctx.Err()
			}
		}),
		jobfx.Module(),
		fx.NopLogger,
	)
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

	fmt.Printf("processed: %d\n", result.Job.Processed)

	return result.ExitCode()
}
```

Run against the local checkout containing App (replace the absolute path):

```sh
go mod init example.com/jobfx-demo
go mod edit -replace=github.com/uchaloop/jobfx=/absolute/path/to/jobfx
GOWORK=off go mod tidy
GOWORK=off go run .
```

Expected stdout is `processed: 3`, with exit code 0. After the App release,
install its published version instead of using the local replacement.

Keep `os.Exit` outside `run` so its deferred cleanup completes.
`Module()` is explicit and must appear exactly once. It provides the runner;
`App.Start` executes work after all startup hooks succeed.

## Run the process

`MakeApp(...).Start(ctx)` synchronously constructs Fx, starts dependencies,
runs one job attempt and optionally observes its result. It does not parse CLI.
`New(application)` additionally delegates CLI handling to the supplied application
before constructing Fx. Provide a `job.Handler`
through Fx to observe every attempted job exactly once. CLI or startup failure
never invokes it. `Stop()` cleans up with a separate context.

| Result | Meaning |
| --- | --- |
| `err != nil` | CLI, graph or startup failure; no job attempt |
| `result.IsCLIHandled` | Help/describe completed; `Job` is nil |
| `result.Job != nil` | Job attempted; inspect its outcome and error |

`Result.ExitCode()` returns 0 for successful CLI or `OutcomeOK`, otherwise 1.
Handle the `Start` error first. Cleanup errors are returned by `Stop`, cached on
repeated calls and do not alter the business result. Neither helper exits the
process or emits a synthetic Fx termination event on successful completion.

Startup, observation and cleanup default to **30s / 10s / 30s**.
To customize the unused App from the quick start, before Start and the cleanup defer:

```go
app = app.WithConfig(jobfx.AppConfig{
	StartupTimeout: 45 * time.Second,
	ObserveTimeout: 15 * time.Second,
	CleanupTimeout: 45 * time.Second,
})
```

Zero selects the default; negative values fail at `Start`. These settings are
independent of `job.Config.Timeout` / `JOB_TIMEOUT` and Fx's Run timeout options.
SIGINT, SIGTERM and Fx shutdown requests cancel startup/work. Observation and
cleanup keep context values but get fresh deadlines. Cancellation is cooperative;
a handler or job that ignores its context can delay completion. Fx cannot force
an uncooperative lifecycle hook to release resources on timeout.

An App is single-use and must not be copied. Defer `Stop` before calling `Start`,
including for errors or panics. Stop before Start prevents startup. Stop during
Start or another Stop returns `ErrAppBusy`; cancel work and wait for Start to
return first. Repeated Start returns `ErrAppUsed`. `WithConfig` clones only an
unused App; cloning a used App defers `ErrAppUsed` to its Start. User panics are
not recovered; use job's recovery middleware when required.

For a scheduler or fully manual lifecycle, keep using
`fx.New(..., jobfx.Module(), fx.Populate(&runner))`, then start dependencies,
call `runner.Run(ctx)` when needed and stop dependencies yourself. `Module`
remains passive and has no implicit CLI handling.

## Optional confx support

[Confx](https://github.com/uchaloop/confx) is an optional integration for typed
configuration, diagnostics and help/describe. The root jobfx module does not
depend on it; use `MakeApp` as above when these features are unnecessary.

To enable this integration, pass a lazy `confx.App` to `jobfx.New`. Config declarations precede ordinary library and domain modules:

```go
app := jobfx.New(confx.New(
    confx.Provide[job.Config]("job"),
    confx.Provide[database.Config]("database"),
    confx.Provide[catalog.Config]("catalog"),
    confx.Runtime(database.Module, jobfx.Module()),
    confx.Runtime(catalog.Module, observer.Module),
))
```

This is a composition fragment, not a standalone program. Import
`github.com/uchaloop/confx` in addition to the quick-start imports.
Here `database`, `catalog` and `observer` are application modules: catalog provides
`job.Func`, observer optionally provides `job.Handler`. Use the same Start/Stop
sequence as above, but check `result.IsCLIHandled` before accessing `result.Job`:

```go
if result.IsCLIHandled {
	return result.ExitCode()
}
```

Place this check after handling the Start error in `run()`. `-help` and `-describe=env|markdown|json` run before Fx
construction and environment loading. Unknown flags return an error.
`JOB_TIMEOUT=4m` configures the job independently of lifecycle budgets.
Confx loader options, engines, diagnostics and manifest options remain available
on the composition before passing it to jobfx. This requires the new
`confx.App.HandleCLI` API, not confx v0.6.2.

## Reference

See [executable example](example_test.go), [API reference](https://pkg.go.dev/github.com/uchaloop/jobfx),
[confx integration checks](CONTRIBUTING.md#confx-integration), [CHANGELOG](CHANGELOG.md)
and [CONTRIBUTING](CONTRIBUTING.md).

### Related libraries

| Library | Purpose |
|---|---|
| [job](https://github.com/uchaloop/job) | Execution contract |
| [beatfx](https://github.com/uchaloop/beatfx) | Run recurring work in an Fx daemon |

## Acknowledgements

Thanks to the authors and maintainers of [Uber Fx](https://github.com/uber-go/fx)
for dependency injection and lifecycle primitives that make this adapter possible.

## License

[MIT](LICENSE)
