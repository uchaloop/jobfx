module github.com/uchaloop/jobfx/integration/confx

go 1.27.0

require (
	github.com/uchaloop/confmaker/v2 v2.1.1
	github.com/uchaloop/confx v0.6.2
	github.com/uchaloop/job v0.4.0
	github.com/uchaloop/jobfx v0.1.2
	github.com/uchaloop/secret/v2 v2.2.0
	go.uber.org/fx v1.24.0
)

require (
	github.com/uchaloop/utilfx v0.2.0 // indirect
	github.com/uchaloop/validate v0.1.0 // indirect
	go.uber.org/dig v1.19.0 // indirect
	go.uber.org/multierr v1.11.0 // indirect
	go.uber.org/zap v1.28.0 // indirect
	golang.org/x/sys v0.48.0 // indirect
)

replace github.com/uchaloop/jobfx => ../..

// HandleCLI is not published yet; use the sibling checkout during development.
replace github.com/uchaloop/confx => ../../../confx
