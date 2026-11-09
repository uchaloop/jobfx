package jobfx

import (
	"context"
	"errors"

	"github.com/uchaloop/job/assignment"
)

// AssignmentSource supplies bootstrap parameters before Fx is constructed.
// It runs only after CLI handling and must honor ctx cancellation. It must not
// depend on the main Fx graph. ScheduledAt must identify the original scheduled
// invocation, including on retries; do not replace it with the current time.
// Source closures are shared by cloned Apps and must support that reuse.
// No source timeout or signal subscription is installed before Fx; sources must
// bound their own I/O. Execute supplies a background context during this phase.
type AssignmentSource func(context.Context) (assignment.Config, assignment.Invocation, error)

var (
	// ErrAssignmentSourceIsNil rejects an explicitly configured nil source.
	ErrAssignmentSourceIsNil = errors.New("assignment source is nil")
	// ErrAssignmentAlreadySet rejects multiple assignment settings.
	ErrAssignmentAlreadySet = errors.New("assignment is already configured")
)

// ExecuteOption configures the App used by Execute, independently of Fx options.
// Options may accompany one Application, one App, or a list of fx.Option values.
type ExecuteOption interface {
	applyExecute(*App) *App
}

type assignmentOption struct {
	source AssignmentSource
}

// WithAssignment enables lazy cluster selection. Nil sources and duplicate
// settings fail after CLI handling, so help/describe remain available. This
// selects a cluster, not one replica; it provides no locking or failover.
func WithAssignment(source AssignmentSource) ExecuteOption {
	return assignmentOption{source: source}
}

func (o assignmentOption) applyExecute(app *App) *App {
	return app.WithAssignment(o.source)
}

// WithAssignment returns an independent unused App sharing the source callback.
// Repeated assignment settings are rejected after CLI, rather than overwritten.
// Cloning an already used App defers ErrAppUsed to Start, as WithConfig does.
func (a *App) WithAssignment(source AssignmentSource) *App {
	clone := a.clone()
	if clone.isAssignmentSet {
		clone.assignmentErr = ErrAssignmentAlreadySet
	}
	clone.isAssignmentSet = true
	clone.assignmentSource = source

	return clone
}

func (a *App) decideAssignment(ctx context.Context) (*assignment.Decision, error) {
	if !a.isAssignmentSet {
		return nil, nil
	}

	if a.assignmentErr != nil {
		return nil, a.assignmentErr
	}

	if a.assignmentSource == nil {
		return nil, ErrAssignmentSourceIsNil
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	cfg, invocation, err := a.assignmentSource(ctx)
	if err != nil {
		return nil, err
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	rotation, err := assignment.MakeRotation(cfg)
	if err != nil {
		return nil, err
	}

	decision, err := rotation.Decide(invocation)
	if err != nil {
		return nil, err
	}

	return &decision, nil
}
