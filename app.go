package jobfx

import (
	"context"
	"errors"
	"io"
	"os"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/uchaloop/job"
	"go.uber.org/fx"
)

// Application supplies lazy composition and CLI handling. *confx.App implements
// this interface without jobfx depending on confx. Implementations must allow
// reuse when an unused App is cloned with WithConfig.
type Application interface {
	FX() fx.Option
	HandleCLI(args []string, output io.Writer) (isHandled bool, err error)
}

// AppConfig controls lifecycle budgets, independently of job.Config.Timeout.
// Zero values select 30s startup, 10s observation and 30s cleanup. Negative
// values are rejected by Start. Cancellation remains cooperative.
type AppConfig struct {
	StartupTimeout time.Duration
	ObserveTimeout time.Duration
	CleanupTimeout time.Duration
}

// Result distinguishes a completed CLI request from an attempted job.
// Job is nil for CLI requests and startup errors.
type Result struct {
	IsCLIHandled bool
	Job          *job.Result
}

// ExitCode returns zero for successful CLI or an OK job, and one otherwise.
// Callers must handle Start errors separately. Cleanup does not change this code.
func (r Result) ExitCode() int {
	if r.IsCLIHandled && r.Job == nil {
		return 0
	}

	if !r.IsCLIHandled && r.Job != nil && r.Job.Outcome == job.OutcomeOK {
		return 0
	}

	return 1
}

var (
	// ErrAppUsed means Start or cloning was attempted after the first Start/Stop.
	ErrAppUsed = errors.New("application has already been used")
	// ErrAppBusy means Stop overlapped Start or another Stop.
	ErrAppBusy = errors.New("application is busy")
	// ErrApplicationIsNil rejects a nil or typed nil composition.
	ErrApplicationIsNil = errors.New("application is nil")
	// ErrContextIsNil rejects a nil Start context.
	ErrContextIsNil = errors.New("context is nil")
	// ErrInvalidTimeout rejects a negative lifecycle timeout.
	ErrInvalidTimeout = errors.New("timeout must not be negative")
)

type appState uint8

const (
	appUnused appState = iota
	appActive
	appReady
	appCleanup
	appStopped
)

// App owns one synchronous job attempt and its Fx dependencies. Do not copy it.
// Start may be called once. Stop must be deferred by the caller, even on errors
// or panics. Stop during Start returns ErrAppBusy; cancel the Start context to
// interrupt work, then Stop after Start returns. Concurrent Stop also returns
// ErrAppBusy. Sequential Stop calls return the original cleanup error.
// The zero value rejects Start with ErrApplicationIsNil. User panics propagate.
type App struct {
	mu            sync.Mutex
	application   Application
	config        AppConfig
	state         appState
	configErr     error
	stopErr       error
	fxApp         *fx.App
	cleanupBase   context.Context
	cancelWork    context.CancelFunc
	bridgeDone    chan struct{}
	bridgeStopped chan struct{}
}

// New stores a lazy application. It does not parse CLI, build Fx or load config.
// Nil applications are rejected at Start. New is an agreed naming exception.
// Include Module in the composition exactly once; it is not added implicitly.
func New(application Application) *App {
	return &App{application: application}
}

// MakeApp stores ordinary Fx options without CLI support. Options are copied;
// their referenced values remain caller-owned. Include Module explicitly.
func MakeApp(options ...fx.Option) *App {
	return New(rawApplication{options: slices.Clone(options)})
}

// WithConfig returns an independent unused App with replacement settings.
// The composition is shared. Cloning an already used App produces an App whose
// Start returns ErrAppUsed. Validation is deferred to Start to preserve laziness.
func (a *App) WithConfig(cfg AppConfig) *App {
	a.mu.Lock()
	defer a.mu.Unlock()

	clone := &App{application: a.application, config: cfg, configErr: a.configErr}
	if a.state != appUnused {
		clone.configErr = ErrAppUsed
	}

	return clone
}

// Start handles process CLI first, then starts dependencies, runs the job once
// and delivers its result to an optional job.Handler. Business failures are in
// Result.Job, not the returned error. CLI/build/start failures have no Job.
// Observation preserves context values but has an independent timeout. Start
// does not call os.Exit, Fx.Run or Shutdowner on normal job completion.
func (a *App) Start(ctx context.Context) (Result, error) {
	a.mu.Lock()
	if a.state != appUnused {
		a.mu.Unlock()

		return Result{}, ErrAppUsed
	}
	a.state = appActive
	a.mu.Unlock()

	defer func() {
		a.mu.Lock()
		a.state = appReady
		a.mu.Unlock()
	}()

	if err := a.validate(ctx); err != nil {
		return Result{}, err
	}

	a.cleanupBase = context.WithoutCancel(ctx)
	isHandled, err := a.application.HandleCLI(os.Args[1:], os.Stdout)
	if err != nil || isHandled {
		return Result{IsCLIHandled: isHandled && err == nil}, err
	}

	var deps struct {
		fx.In
		Runner  *job.Runner
		Handler job.Handler `optional:"true"`
	}
	a.fxApp = fx.New(a.application.FX(), fx.Populate(&deps))
	if err := a.fxApp.Err(); err != nil {
		return Result{}, err
	}

	workCtx, cancelWork := context.WithCancel(ctx)
	a.cancelWork = cancelWork
	a.bridgeDone = make(chan struct{})
	a.bridgeStopped = make(chan struct{})
	shutdown := a.fxApp.Wait()
	go func() {
		defer close(a.bridgeStopped)

		select {
		case <-shutdown:
			cancelWork()
		case <-a.bridgeDone:
		}
	}()

	if err := a.startDependencies(workCtx); err != nil {
		return Result{}, err
	}

	result := deps.Runner.Run(workCtx)
	if deps.Handler != nil {
		a.observe(a.cleanupBase, deps.Handler, result)
	}

	return Result{Job: &result}, nil
}

// Stop releases dependencies with an independent cleanup budget and joins the
// signal bridge. Stop before Start is safe and prevents subsequent Start.
// Cleanup errors are returned and cached; they do not alter the job outcome.
func (a *App) Stop() error {
	a.mu.Lock()
	switch a.state {
	case appActive, appCleanup:
		a.mu.Unlock()

		return ErrAppBusy
	case appStopped:
		err := a.stopErr
		a.mu.Unlock()

		return err
	}
	a.state = appCleanup
	a.mu.Unlock()

	var err error
	defer func() {
		a.releaseBridge()
		a.mu.Lock()
		a.stopErr = err
		a.state = appStopped
		a.mu.Unlock()
	}()

	if a.fxApp == nil {
		return nil
	}

	ctx, cancel := context.WithTimeout(a.cleanupBase, a.config.CleanupTimeout)
	defer cancel()

	err = a.fxApp.Stop(ctx)

	return err
}

func (a *App) validate(ctx context.Context) error {
	if a.configErr != nil {
		return a.configErr
	}

	if ctx == nil {
		return ErrContextIsNil
	}

	if a.application == nil {
		return ErrApplicationIsNil
	}

	value := reflect.ValueOf(a.application)
	switch value.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
		if value.IsNil() {
			return ErrApplicationIsNil
		}
	}

	if a.config.StartupTimeout < 0 || a.config.ObserveTimeout < 0 || a.config.CleanupTimeout < 0 {
		return ErrInvalidTimeout
	}

	if a.config.StartupTimeout == 0 {
		a.config.StartupTimeout = 30 * time.Second
	}

	if a.config.ObserveTimeout == 0 {
		a.config.ObserveTimeout = 10 * time.Second
	}

	if a.config.CleanupTimeout == 0 {
		a.config.CleanupTimeout = 30 * time.Second
	}

	return nil
}

func (a *App) startDependencies(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, a.config.StartupTimeout)
	defer cancel()

	return a.fxApp.Start(ctx)
}

func (a *App) observe(ctx context.Context, handler job.Handler, result job.Result) {
	ctx, cancel := context.WithTimeout(ctx, a.config.ObserveTimeout)
	defer cancel()

	handler.Handle(ctx, result)
}

func (a *App) releaseBridge() {
	if a.cancelWork == nil {
		return
	}

	close(a.bridgeDone)
	a.cancelWork()
	<-a.bridgeStopped
}

type rawApplication struct {
	options []fx.Option
}

var _ Application = rawApplication{}

func (a rawApplication) FX() fx.Option {
	return fx.Options(a.options...)
}

func (rawApplication) HandleCLI([]string, io.Writer) (bool, error) {
	return false, nil
}
