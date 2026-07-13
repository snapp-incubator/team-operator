# Contributing

## Building

```bash
make build
```

No private dependencies are required. The binary includes all features except
real-time IAM admin watch (see below).

## Running tests

```bash
go test ./...
```

Tests do not require the IAM SDK or any private dependencies.

## Optional: real-time IAM admin watch (`-tags iam`)

The `--enable-iam-team-admin-access` flag enables two sub-features:

| Sub-feature | Build tag required |
|---|---|
| Fetch team admins from IAM HTTP API on every reconcile | none — always compiled |
| Real-time watch: trigger immediate reconcile when IAM admins change | `-tags iam` |

The watch feature depends on an internal Snapp SDK
(`gitlab.snapp.ir/platform/iam-sdk/go`) that is not publicly available.
If you work at Snapp and have the SDK checked out locally, set up a Go
workspace once:

```bash
cp go.work.example go.work
# Edit go.work and set the path that matches your local checkout of the SDK
```

Then build with:

```bash
make build-iam
```

`go.work` is gitignored and is never committed. Every developer keeps their own
copy with the path that matches their machine.

> **Note on `go mod tidy`:** running `go mod tidy` without the IAM SDK available
> will fail because Go's tidy command processes all build-constrained files,
> including `internal/iam/watcher_sdk.go`. External contributors should skip
> `go mod tidy` or run it only after setting up `go.work` with the SDK path.
