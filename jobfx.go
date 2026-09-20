/*
Package jobfx provides a job.Runner through Uber Fx. It is an independent
module; applications using job directly do not depend on Fx.

[Module] builds the Runner from what the container holds - a job.Config, a
job.Func, and an optional ordered [Options]:

	fx.New(
		confx.Module(),
		confx.Provide[job.Config](),

		fx.Provide(func(db *sql.DB) job.Func {
			return func(ctx context.Context) (int, error) { return drain(ctx, db) }
		}),

		jobfx.Module(),
	)

# It does not run the work

That is the whole point of the split. A scheduler drives the Runner in a loop; a
one-shot process runs it once and exits. Neither is something an Fx hook should
decide, so Module provides the Runner and stops there: no OnStart that runs the
work, no Shutdowner, no exit code.

A one-shot process therefore owns its own sequence, and owns it explicitly:

	app.Start(startupCtx)   // dependencies first - no work before they are ready
	result := runner.Run(workCtx)   // workCtx is cancelled by SIGTERM
	handler.Handle(observeCtx, result)
	app.Stop(cleanupCtx)    // a fresh budget, not the cancelled work context
	os.Exit(code(result))

Running the work inside OnStart would fold its duration into the startup
timeout, and fx.Shutdowner would report an ordinary completion as a termination
signal. Both read as something going wrong when nothing did.

Subscribe to app.Wait before Start and cancel the startup/work context when Fx
receives a shutdown request. Keep that subscription until cleanup completes.
Cleanup gets an independent context. Cancellation remains cooperative: the
application must wait for work to return before closing its dependencies.
See ExampleModule for the complete sequence.

# What the exit code says

It tells the scheduler whether to try this work again, so only the attempt
decides it: a non-ok Outcome is a non-zero code. A failure to flush telemetry
after successful work is logged and reported, but must not raise the code -
rerunning would repeat the business effect to fix an observability problem. An
application that does want a failed flush to count can say so, as long as it
says so deliberately.

# Why one value and not a value group

Fx produces the values of a group in an unspecified order, so two options that
set the same thing resolve arbitrarily. [Options] arrives as one ordered value
instead: options passed to Module are applied first, then the container's, each
in the order it was written, and the last write wins predictably.
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
//		return jobfx.Options{job.WithMiddleware(recovery.Middleware(recovery.WithLogger(log)))}
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
