# Changelog

## [0.2.2] - 2026-11-09

### Deprecated

- Deprecated this module in favor of `github.com/uchaloop/beatfx/jobfx`.
  See README for import-path migration. Existing API and runtime behavior remain unchanged.

## [0.2.1] - 2026-11-09

### Added

- Added `Execute` as a process entry point accepting Fx options, an Application
  such as confx.App, or a configured jobfx.App. It exits only after cleanup and
  adds no logging; cleanup errors do not change the job exit code.

- Added lazy multicluster assignment through `WithAssignment` and App settings.
  CLI runs first; non-owner clusters exit successfully before Fx construction.
  Assignment decisions are available in Result without fabricating a job attempt.

### Fixed

- Copied Module option slices so caller mutations cannot change a deferred Fx
  composition or its reuse. Referenced option state remains shared.

## [0.2.0] - 2026-11-09

### Added

- Added lazy `App`, `New` and `MakeApp` for one-shot work with explicit Start/Stop.
- Added CLI-aware results and optional result observation, with configurable
  startup/observation/cleanup defaults of 30/10/30 seconds.
- Added cancellation bridging without synthetic termination on successful work.

### Changed

- Updated job to v0.4.0 and adapted examples and tests to its renamed assignment
  fields and recovery constructor. Panic diagnostics now omit raw values and stacks.

## [0.1.2] - 2026-10-01

- Update dependencies

## [0.1.1] - 2026-09-22

- Update package documentation, README examples and tests for job's `int64`
  processed-item counts. DI wiring and lifecycle behavior are unchanged.

## [0.1.0] - 2026-09-20

- Initial standalone release of the jobfx Uber Fx adapter.
- Independent module, dependencies, tests and release workflow.

[0.1.2]: https://github.com/uchaloop/jobfx/compare/v0.1.1...v0.1.2
[0.1.1]: https://github.com/uchaloop/jobfx/compare/v0.1.0...v0.1.1
[0.1.0]: https://github.com/uchaloop/jobfx/releases/tag/v0.1.0

[0.2.0]: https://github.com/uchaloop/jobfx/compare/v0.1.2...v0.2.0

[0.2.1]: https://github.com/uchaloop/jobfx/compare/v0.2.0...HEAD
