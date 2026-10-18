# Contributing

Thanks for helping. keyroll touches people's API keys, so the bar is: **never print, log or send a key value**, and never write a file in a way that could lose data.

## Before you open a PR
- Read [GOAL.md](GOAL.md). PRs outside it are closed with a pointer there.
- `gofmt -l .` is empty, `go vet ./...` is clean, `go test -race ./...` passes.
- New key formats: add the pattern in `internal/provider`, a fake key in `internal/testhome` (assembled at run time, never a literal), and a test.
- New provider check endpoints: must be free, read-only, on the provider's own API host, with the key in a header. Record the real answer to an obviously fake key in `internal/check/testdata/<id>.json` and make `TestRecordedProviderAnswersAreRejected` cover it.
- New locations: cite where the tool documents or writes that file (source link or docs) in the PR.

## Tests that must keep passing
- `assertNoLeak` in `cmd/keyroll/main_test.go` greps every output mode for any 8-character run of every fake key.
- `scripts/integration.sh` runs the real Codex CLI and `llm` CLI, rotates, and checks the tools read the new key back.

## Reporting a security problem
Please do not open a public issue; use GitHub's private vulnerability reporting on this repository.
