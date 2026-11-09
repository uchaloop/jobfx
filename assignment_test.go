package jobfx

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/uchaloop/job"
	"github.com/uchaloop/job/assignment"
	"go.uber.org/fx"
)

func assignmentFixture(current string) AssignmentSource {
	return func(context.Context) (assignment.Config, assignment.Invocation, error) {
		return assignment.Config{Clusters: []string{"b", "a"}, Current: current, Period: time.Minute},
			assignment.Invocation{ScheduledAt: time.Unix(0, 0)}, nil
	}
}

func TestAssignmentSkipsBeforeFx(t *testing.T) {
	app := MakeApp(fx.Invoke(func() { t.Error("constructed Fx for skipped cluster") })).WithAssignment(assignmentFixture("b"))
	for range 2 {
		clone := app.WithConfig(AppConfig{})
		result, err := clone.Start(context.Background())
		if err != nil || result.Assignment == nil || result.Assignment.Owner != "a" || result.Assignment.IsExecutable || result.Job != nil || result.ExitCode() != 0 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		if err := clone.Stop(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAssignmentCLIComesFirst(t *testing.T) {
	for _, app := range []*App{
		New(&executeApplication{isHandled: true}).WithAssignment(nil),
		New(&executeApplication{isHandled: true}).WithAssignment(func(context.Context) (assignment.Config, assignment.Invocation, error) {
			t.Error("source ran for CLI")

			return assignment.Config{}, assignment.Invocation{}, errors.New("source failed")
		}),
		New(&executeApplication{isHandled: true}).WithAssignment(nil).WithAssignment(nil),
	} {
		result, err := app.Start(context.Background())
		if err != nil || !result.IsCLIHandled || result.Assignment != nil || result.Job != nil {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		if err := app.Stop(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAssignmentFailuresDoNotConstructFx(t *testing.T) {
	want := errors.New("source failed")
	for _, test := range []struct {
		name   string
		source AssignmentSource
		want   error
	}{
		{name: "nil source", want: ErrAssignmentSourceIsNil},
		{name: "source error", source: func(context.Context) (assignment.Config, assignment.Invocation, error) {
			return assignment.Config{}, assignment.Invocation{}, want
		}, want: want},
	} {
		t.Run(test.name, func(t *testing.T) {
			app := MakeApp(fx.Invoke(func() { t.Error("constructed Fx") })).WithAssignment(test.source)
			result, err := app.Start(context.Background())
			if !errors.Is(err, test.want) || result.Job != nil || result.Assignment != nil || result.ExitCode() != 1 {
				t.Fatalf("result=%+v err=%v", result, err)
			}
			if err := app.Stop(); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestAssignmentSelectedOrder(t *testing.T) {
	var phases []string
	app := MakeApp(
		fx.NopLogger,
		fx.Supply(job.Config{}),
		fx.Provide(func() job.Func {
			phases = append(phases, "construct")

			return func(context.Context) (int64, error) {
				phases = append(phases, "work")

				return 7, nil
			}
		}),
		Module(),
		fx.Invoke(func(lc fx.Lifecycle) {
			lc.Append(fx.Hook{
				OnStart: func(context.Context) error {
					phases = append(phases, "start")

					return nil
				},
				OnStop: func(context.Context) error {
					phases = append(phases, "stop")

					return nil
				},
			})
		}),
	).WithAssignment(func(ctx context.Context) (assignment.Config, assignment.Invocation, error) {
		phases = append(phases, "assignment")

		return assignmentFixture("a")(ctx)
	})
	result, err := app.Start(context.Background())
	if err != nil || result.Assignment == nil || !result.Assignment.IsExecutable || result.Job == nil || result.Job.Processed != 7 || result.ExitCode() != 0 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(phases, []string{"assignment", "construct", "start", "work", "stop"}) {
		t.Fatal(phases)
	}
}

func TestAssignmentCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	app := MakeApp(fx.Invoke(func() { t.Error("constructed Fx") })).WithAssignment(func(ctx context.Context) (assignment.Config, assignment.Invocation, error) {
		cancel()

		return assignmentFixture("b")(ctx)
	})
	result, err := app.Start(ctx)
	if !errors.Is(err, context.Canceled) || result.Assignment != nil || result.ExitCode() != 1 {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestExecuteAssignmentOptions(t *testing.T) {
	options := fx.Options(fx.Invoke(func() { t.Error("constructed Fx for skip") }))
	for _, inputs := range [][]any{
		{options, WithAssignment(assignmentFixture("b"))},
		{WithAssignment(assignmentFixture("b")), MakeApp(options)},
		{New(&executeApplication{isHandled: true}), WithAssignment(nil)},
	} {
		if code := execute(inputs...); code != 0 {
			t.Fatal(code)
		}
	}
	for _, inputs := range [][]any{
		{options, WithAssignment(nil)},
		{options, WithAssignment(assignmentFixture("b")), WithAssignment(assignmentFixture("b"))},
		{MakeApp(options).WithAssignment(assignmentFixture("b")), WithAssignment(assignmentFixture("b"))},
		{WithAssignment(assignmentFixture("b"))},
	} {
		if code := execute(inputs...); code != 1 {
			t.Fatal(code)
		}
	}
}

func TestAssignmentRejectsInvalidRotationAndInvocation(t *testing.T) {
	for _, source := range []AssignmentSource{
		func(context.Context) (assignment.Config, assignment.Invocation, error) {
			return assignment.Config{}, assignment.Invocation{ScheduledAt: time.Unix(0, 0)}, nil
		},
		func(context.Context) (assignment.Config, assignment.Invocation, error) {
			return assignment.Config{Clusters: []string{"a"}, Current: "a", Period: time.Minute}, assignment.Invocation{}, nil
		},
	} {
		app := MakeApp(fx.Invoke(func() { t.Error("invalid assignment constructed Fx") })).WithAssignment(source)
		result, err := app.Start(context.Background())
		if err == nil || result.Assignment != nil || result.Job != nil || result.ExitCode() != 1 {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		if err := app.Stop(); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAssignmentCanceledBeforeSource(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	app := MakeApp().WithAssignment(func(context.Context) (assignment.Config, assignment.Invocation, error) {
		t.Error("source ran after cancellation")

		return assignment.Config{}, assignment.Invocation{}, nil
	})
	if _, err := app.Start(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
}

func TestAssignmentResultConsistency(t *testing.T) {
	for _, result := range []Result{
		{IsCLIHandled: true, Assignment: &assignment.Decision{}},
		{Assignment: &assignment.Decision{IsExecutable: true}},
		{Assignment: &assignment.Decision{}, Job: &job.Result{Outcome: job.OutcomeOK}},
	} {
		if code := result.ExitCode(); code != 1 {
			t.Fatal(code)
		}
	}
}

func TestAssignmentUsedAppAndDuplicate(t *testing.T) {
	app := MakeApp().WithAssignment(assignmentFixture("b"))
	if err := app.Stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := app.WithAssignment(assignmentFixture("b")).Start(context.Background()); !errors.Is(err, ErrAppUsed) {
		t.Fatal(err)
	}

	duplicate := MakeApp().WithAssignment(assignmentFixture("b")).WithConfig(AppConfig{}).WithAssignment(assignmentFixture("a"))
	if _, err := duplicate.Start(context.Background()); !errors.Is(err, ErrAssignmentAlreadySet) {
		t.Fatal(err)
	}
	if err := duplicate.Stop(); err != nil {
		t.Fatal(err)
	}
}
