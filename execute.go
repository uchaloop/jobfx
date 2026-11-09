package jobfx

import (
	"context"
	"os"

	"go.uber.org/fx"
)

// Execute owns a one-shot process and exits after cleanup. Pass either one or
// more fx.Option values, one Application (such as *confx.App), or one unused
// *App with custom settings. ExecuteOption values may accompany any mode.
// Mixed composition modes, empty composition and nil inputs exit with
// code 1 before startup. Assignment settings are validated after CLI.
// To expand []fx.Option, pass fx.Options(options...).
//
// Execute uses context.Background and the App's signal handling. Successful
// CLI, skipped clusters and successful work exit with code 0. Invalid input,
// assignment/startup errors and unsuccessful work exit with code 1. Cleanup
// errors do not change that code.
// Execute adds no logs and intentionally discards cleanup errors; use Start/Stop
// to inspect errors. Configured Fx loggers and application handlers still run.
//
// Cleanup completes before os.Exit. Caller defers do not run on normal exit.
// User panics propagate after deferred cleanup; Execute does not recover them.
// Execute is an explicitly agreed process-entry exception to the no-exit rule.
func Execute(inputs ...any) {
	os.Exit(execute(inputs...))
}

func execute(inputs ...any) int {
	app := makeExecuteApp(inputs)
	if app == nil {
		return 1
	}

	defer func() {
		// Preserve the job outcome and keep this process helper silent.
		_ = app.Stop()
	}()

	result, err := app.Start(context.Background())
	if err != nil {
		return 1
	}

	return result.ExitCode()
}

func makeExecuteApp(inputs []any) *App {
	composition := make([]any, 0, len(inputs))
	var settings []ExecuteOption
	for _, input := range inputs {
		if isNilInput(input) {
			return nil
		}

		if option, isOK := input.(ExecuteOption); isOK {
			settings = append(settings, option)

			continue
		}

		composition = append(composition, input)
	}

	app := makeCompositionApp(composition)
	if app == nil {
		return nil
	}

	for _, option := range settings {
		app = option.applyExecute(app)
	}

	return app
}

func makeCompositionApp(inputs []any) *App {
	if len(inputs) == 0 {
		return nil
	}

	if len(inputs) == 1 {
		switch input := inputs[0].(type) {
		case *App:
			return input
		case Application:
			return New(input)
		}
	}

	options := make([]fx.Option, 0, len(inputs))
	for _, input := range inputs {
		option, isOK := input.(fx.Option)
		if !isOK {
			return nil
		}

		options = append(options, option)
	}

	return MakeApp(options...)
}
