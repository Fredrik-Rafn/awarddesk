# Tests

Tests are split by what they check, rather than forcing Go's private functions
to become public just so a test can reach them.

```text
tests/
  integration/     HTTP/admin contracts, jobs + SQLite, concurrent scheduling
  browser/         Frontend JavaScript tests run locally with Node
internal/**/
  *_test.go        Unit tests for that package's private logic
  testdata/        Small local fixtures used by those unit tests
cmd/**/
  *_test.go        Unit tests for diagnostic output formatting
```

## Run everything offline

From the repository root:

```sh
go test -p 1 ./...
go vet -p 1 ./...
```

These tests use temporary databases, local fixtures and fake providers. Some HTTP
adapter tests start a local test server. They do not log in to EVA, read a real
mailbox, create accounts or spend solver credits. No production database or `.env`
is needed.

The JavaScript suite uses Node's built-in test runner and a simulated DOM. It
does not launch or automate a browser. Node is optional: Go reports that suite as
skipped if Node is unavailable; install Node to include those checks.

## Run one group

```sh
# Unit tests beside the Go packages
go test -p 1 ./internal/... ./cmd/...

# Integration tests through exported application interfaces
go test -p 1 ./tests/integration

# Frontend JavaScript checks
go test -count=1 ./tests/browser
```

HTTP integration tests obtain request tokens from `/api/config`, just like the
frontend. Shared test setup lives in `helpers_test.go` and `jobs_helpers_test.go`;
it is not part of the application binary.

## Adding tests

- Put a small test of private parsing, retry or matching logic beside its Go file.
- Put a cross-package API, SQLite or scheduling scenario in `integration/`.
- Put a frontend behavior test in `browser/`. Add its filename to the small Go
  wrapper there so `go test ./...` continues to run it.
- Keep fixtures synthetic or sanitized. Never commit real credentials, cookies,
  mail contents or booking/session tokens.

The commands under `cmd/eva-login`, `cmd/outlook-check` and `cmd/eva-check` are
explicit live diagnostics, not part of the automated test suite. Running those
separately can contact providers and consume credits.
