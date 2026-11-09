# jobfx

> **Deprecated:** Use [`github.com/uchaloop/beatfx/jobfx`](https://pkg.go.dev/github.com/uchaloop/beatfx/jobfx) instead.
> The implementation now lives in beatfx. See [Migration](#migration).

[![Go Reference](https://pkg.go.dev/badge/github.com/uchaloop/jobfx.svg)](https://pkg.go.dev/github.com/uchaloop/jobfx) [![CI](https://github.com/uchaloop/jobfx/actions/workflows/ci.yml/badge.svg)](https://github.com/uchaloop/jobfx/actions/workflows/ci.yml) [![Release](https://img.shields.io/github/v/tag/uchaloop/jobfx?label=release)](https://github.com/uchaloop/jobfx/tags) [![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

**Optional Uber Fx integration for [job](https://github.com/uchaloop/job).**
Job owns work execution; jobfx supplies Fx wiring and the optional one-shot App.
Importing job alone does not require Fx.

[Install](#installation) · [Quick start](#quick-start) · [Lifecycle](#run-the-process) · [Multicluster](#multicluster-assignment) · [Confx support](#optional-confx-support) · [Reference](#reference)

## Migration

Update related imports together:

| Old import | Replacement |
|---|---|
| `github.com/uchaloop/jobfx` | `github.com/uchaloop/beatfx/jobfx` |
| `github.com/uchaloop/job` | `github.com/uchaloop/beat/job` |
| `github.com/uchaloop/job/assignment` | `github.com/uchaloop/beat/assignment` |
| `github.com/uchaloop/job/middleware/recovery` | `github.com/uchaloop/beat/job/middleware/recovery` |

```sh
go get github.com/uchaloop/beat@latest github.com/uchaloop/beatfx@latest
```

The package names remain the same. Old and new named types have different
Go identities: update providers, handlers, middleware and assignment policies
together rather than mixing old and new packages in one Fx graph.
This deprecation does not change the old API or runtime behavior.
See the [replacement documentation](https://github.com/uchaloop/beatfx) for the current API and examples.

## Installation

Requires Go 1.27 or later. The remaining examples document the deprecated
standalone module for existing users. For new code, follow Migration above.

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
	"time"

	"github.com/uchaloop/job"
	"github.com/uchaloop/jobfx"
	"go.uber.org/fx"
)

func main() {
	jobfx.Execute(
		fx.Supply(job.Config{Timeout: 4 * time.Minute}),
		fx.Provide(func() job.Func {
			return func(ctx context.Context) (int64, error) {
				return 3, ctx.Err()
			}
		}),
		fx.Provide(func() job.Handler {
			return job.HandlerFunc(func(_ context.Context, result job.Result) {
				fmt.Printf("processed: %d\n", result.Processed)
			})
		}),
		jobfx.Module(),
		fx.NopLogger,
	)
}
```

Run against the local checkout containing Execute (replace the absolute path):

```sh
go mod init example.com/jobfx-demo
go mod edit -replace=github.com/uchaloop/jobfx=/absolute/path/to/jobfx
GOWORK=off go mod tidy
GOWORK=off go run .
```

Expected stdout is `processed: 3`, with exit code 0. After the v0.2.1 release,
install its published version instead of using the local replacement.

Execute owns process exit and finishes cleanup first. Caller defers do not run
on normal exit. The output above comes from the supplied handler, not Execute.
`Module()` is explicit and must appear exactly once. It provides the runner;
`App.Start` executes work after all startup hooks succeed.

## Execute inputs

`Execute(inputs ...any)` accepts exactly one of these forms:

| Input | Composition |
| --- | --- |
| One or more `fx.Option` values, including `fx.Module(...)` | Ordinary Fx, no CLI parsing |
| One `Application`, such as `*confx.App` | CLI-aware lazy composition |
| One unused `*jobfx.App` | Existing composition and custom lifecycle budgets |

ExecuteOption values such as `WithAssignment(source)` can accompany any of the
three composition forms. They are separate from Fx options and must not be
placed inside `fx.Options` or `confx.Runtime`.

Mixed composition modes, nil input values, empty composition and unsupported types exit silently with
code 1 before startup. To pass a slice, use `Execute(fx.Options(options...))`;
Go cannot expand `[]fx.Option` into `...any` directly.

Execute uses `context.Background()` with App signal cancellation. Help/describe
and skipped clusters or successful work exit with 0. Startup errors and unsuccessful work exit with
1. Cleanup errors are deliberately discarded and do not change that code.
Execute adds no logging; supplied Fx loggers and application handlers retain
their own behavior. Panics propagate after cleanup. For a custom context or
access to startup and cleanup errors, use the manual App API below.

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
| `result.Assignment != nil && !result.Assignment.IsExecutable` | Another cluster owns the invocation; `Job` is nil |
| `result.Job != nil` | Job attempted; inspect its outcome and error |

`Result.ExitCode()` returns 0 for successful CLI, a skipped cluster or `OutcomeOK`,
otherwise 1. Check CLI and assignment skip before dereferencing `result.Job`.
Handle the `Start` error first. Cleanup errors are returned by `Stop`, cached on
repeated calls and do not alter the business result. Start/Stop do not exit the
process. Normal completion does not emit a synthetic Fx termination event.

Startup, observation and cleanup default to **30s / 10s / 30s**.
For custom budgets, pass a configured App to Execute (options are your Fx modules):

```go
jobfx.Execute(jobfx.MakeApp(options...).WithConfig(jobfx.AppConfig{
	StartupTimeout: 45 * time.Second,
	ObserveTimeout: 15 * time.Second,
	CleanupTimeout: 45 * time.Second,
}))
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

## Multicluster assignment

Use `WithAssignment` to select the owning cluster after help/describe but before
`fx.New`, constructors or lifecycle hooks. The algorithm remains in
`job/assignment`; jobfx only controls when it runs.

For an existing `loadAssignment` callback and Fx modules, the entry point is:

```go
func main() {
 jobfx.Execute(
  fx.Supply(job.Config{}),
  storage.Module,
  catalog.Module,
  jobfx.Module(),
  jobfx.WithAssignment(loadAssignment),
 )
}
```

This is an application fragment: storage and catalog are your modules, catalog
provides `job.Func`, and the callback has this signature:

```go
func loadAssignment(ctx context.Context) (
 assignment.Config,
 assignment.Invocation,
 error,
)
```

Import `github.com/uchaloop/job/assignment`. The callback supplies cluster names,
the current cluster, period and the original scheduled invocation. See the
[executable example](example_test.go) for a concrete source and manual lifecycle.
Use `app.WithAssignment(loadAssignment)` to configure an App directly;
`WithConfig` preserves that setting. Source closures remain shared between clones.

With confx, keep the callback outside the configuration composition:

```go
jobfx.Execute(
 confx.New(
  confx.Provide[job.Config]("job"),
  confx.Runtime(storage.Module, catalog.Module, jobfx.Module()),
 ),
 jobfx.WithAssignment(loadAssignment),
)
```

The lifecycle is:

1. Handle CLI. Neither the assignment source nor its validation runs for a
   successfully handled help/describe request.
2. Load bootstrap parameters through the source and call Rotation.Decide.
3. If another cluster owns the slot, return a successful skip without creating
   Fx, loading the runtime configuration, running work or invoking job.Handler.
4. Otherwise construct Fx and execute the existing Start/work/observe/Stop path.

Source errors, invalid rotation settings, missing/off-grid ScheduledAt and
cancellation fail before Fx. A nil source or repeated assignment setting is also
an error, checked after CLI. Execute exits silently with code 1; manual Start
returns the error. Stop after a skip is safe and has no dependency hooks to run.
Assignment decisions are retained in `Result.Assignment`; no job result is
fabricated for a skipped invocation.

The source is bootstrap code, not an Fx provider. It may load a small config
through confmaker, but cannot resolve dependencies from the unbuilt Fx graph.
Its settings do not automatically appear in the confx manifest, and jobfx does
not add a second loader or DI container. CLI descriptions may still invoke user
defaults under the existing confx contract.

The source must honor its context and bound any I/O itself. Lifecycle timeouts
and Fx signal reception begin later; Execute uses a background context during
assignment. A source that blocks can delay startup. Manual Start passes its
caller context to the source.

All participants must use identical cluster topology, period and original
`ScheduledAt`. Retries keep that timestamp; do not substitute `time.Now()`.
Selection chooses a cluster, not one replica, and adds no distributed lock,
failover or duplicate suppression. Skipped decisions are available to manual
callers; Execute adds no logging or separate observation callback for them.

## Optional confx support

[Confx](https://github.com/uchaloop/confx) is an optional integration for typed
configuration, diagnostics and help/describe. The root jobfx module does not
depend on it; pass ordinary Fx options to Execute when these features are
unnecessary.

To enable this integration, pass a lazy `confx.App` directly to `jobfx.Execute`.
Config declarations precede ordinary library and domain modules:

```go
jobfx.Execute(confx.New(
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
`job.Func`, observer optionally provides `job.Handler`. Execute handles CLI and
process exit automatically. For manual lifecycle use
`jobfx.New(composition)`, then Start/Stop; check `result.IsCLIHandled` before
accessing `result.Job`.

`-help` and `-describe=env|markdown|json` run before Fx
construction and environment loading. Unknown flags make Execute exit with
code 1; manual Start returns the error. Description may invoke user defaults,
which can perform I/O.
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
