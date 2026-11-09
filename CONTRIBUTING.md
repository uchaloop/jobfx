# Contributing

Run `GOWORK=off go mod tidy -diff`, `GOWORK=off go build ./...`,
`GOWORK=off go vet ./...` and `GOWORK=off go test -race ./...`.
The separate integration module must be checked explicitly.

`jobfx.New` is the agreed constructor naming exception for the lazy application
entry point. It defers validation to Start. `App.Stop()` intentionally uses an
internally owned cleanup context and budget; it must work after cancellation of
Start's context. `Execute` is the agreed process-entry exception to the no-exit rule. It calls
`os.Exit` only after its internal execution function has completed deferred
cleanup. It intentionally discards cleanup errors without logging. Keep the
manual App API available for callers that need to inspect those errors.

## Platform checks

The SIGTERM subprocess test is Unix-only. Other tests remain portable.
Cross-compile the root tests without running Windows binaries:

```sh
GOWORK=off GOOS=windows GOARCH=amd64 go test -c -o /tmp/jobfx.test.exe
```

## Confx integration

The separate `integration/confx` module checks the real lazy CLI bridge, secret
descriptions and diagnostics. Root jobfx remains independent of confx and
confmaker. Root `go test ./...` does not include this module.

During development, keep jobfx and confx as sibling checkouts containing App
and HandleCLI respectively. After publishing HandleCLI, pin that release, remove
the external confx replacement and enable the integration checks in standalone
CI. The jobfx replacement points to this repository and remains in place.
No go.work is required.

From the repository root run:

```sh
cd integration/confx
GOWORK=off go mod tidy -diff
GOWORK=off go build ./...
GOWORK=off go vet ./...
GOWORK=off go test -race ./...
```
