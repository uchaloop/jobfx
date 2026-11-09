/*
Package jobfx connects job.Runner to Uber Fx. Applications using job directly
remain independent of Fx.

Module provides a Runner from job.Config, job.Func and optional Options. It
never runs work or installs a work OnStart hook. Schedulers and applications
with their own lifecycle can keep using Module with ordinary fx.New.

For one-shot processes, MakeApp accepts Fx options and New accepts an Application
such as *confx.App. Both are lazy. App.Start handles CLI before constructing Fx,
starts dependencies, runs one job, then calls an optional job.Handler. The caller
must defer App.Stop and decide when to exit the process. See ExampleMakeApp.

Startup, observation and cleanup default to 30, 10 and 30 seconds respectively;
App.WithConfig replaces these settings. job.Config.Timeout still controls work.
Observation and cleanup preserve context values with independent cancellation.
All timeouts are cooperative: hooks, work and handlers must honor their contexts.

Normal completion calls Fx.Stop directly without Fx.Run or a synthetic shutdown
signal. SIGINT, SIGTERM and Fx shutdown requests cancel startup/work. Business
failures are recorded in Result.Job; CLI and startup errors are returned separately.
Cleanup errors do not change Result.ExitCode, so failed telemetry cleanup does
not automatically request a retry of successful business work.

Options is one ordered slice rather than an unordered Fx value group. Options
passed to Module come first, then container options; middleware accumulates in
that order, with the first wrapper outermost.
*/
package jobfx

import (
	"github.com/uchaloop/job"
	"go.uber.org/fx"
)

// Options is the ordered set of job options an application builds from the
// container. Provide it once, and the order inside it is the order applied:
//
//	fx.Provide(func(log *slog.Logger) jobfx.Options {
//		return jobfx.Options{job.WithMiddleware(recovery.MakeMiddleware(recovery.WithLogger(log)))}
//	})
//
// A set that needs nothing from the container can be supplied outright with
// fx.Supply(jobfx.Options{...}), though such options can also go straight to
// [Module].
type Options []job.Option

// params are the container dependencies Module consumes. Config and Func are
// required; Options are the container-built options, if the application
// provides any.
type params struct {
	fx.In

	Config  job.Config
	Func    job.Func
	Options Options `optional:"true"`
}

// Module provides a *job.Runner built from the container. It runs nothing: see
// the package documentation for the sequence a one-shot process owns.
//
// Options come from two places and both are applied, in one defined order: the
// static ones passed here first, then [Options] from the container, each in the
// order it was written. Static first is what puts a recovery middleware given to
// Module outside everything the container adds, which is where it can still
// catch a panic from those wrappers.
func Module(opts ...job.Option) fx.Option {
	return fx.Module(
		"job",

		fx.Provide(
			func(p params) (*job.Runner, error) {
				all := make([]job.Option, 0, len(opts)+len(p.Options))
				all = append(all, opts...)
				all = append(all, p.Options...)

				return job.MakeRunner(p.Config, p.Func, all...)
			},
		),
	)
}
