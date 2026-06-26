# Contributing to CairnMark

Thanks for your interest. CairnMark is a small, deliberately-scoped service; the
bar for changes is that they keep it small and sharp.

## Development setup

```sh
# Bring up Postgres + MinIO (and the service, if you want the full stack):
docker compose up -d postgres minio

# Run the service locally against them:
cp .env.example .env   # defaults already match the compose stack
set -a && . ./.env && set +a
go run ./cmd/server
```

## Before opening a PR

```sh
gofmt -l .        # must print nothing
go vet ./...
go build ./...
go test ./...     # unit tests; use the in-memory backend + fake repo
```

Integration tests run against real Postgres + MinIO and are behind a build tag:

```sh
docker compose up -d postgres minio
export CAIRNMARK_POSTGRES_DSN=postgres://cairnmark:cairnmark@localhost:5432/cairnmark?sslmode=disable
export CAIRNMARK_S3_ENDPOINT=localhost:9000 CAIRNMARK_S3_ACCESS_KEY=cairnmark \
       CAIRNMARK_S3_SECRET_KEY=cairnmark-secret CAIRNMARK_S3_BUCKET=cairnmark-it
go test -tags=integration ./...
```

CI runs all of the above on every PR.

## Conventions

The guiding principle is **divide and conquer** — every package owns one concern,
stays small, and talks to the rest through narrow interfaces.

- **Dependencies point inward:** `api → files → { storage, metadata }`. Nothing
  imports `api`; nothing below `files` imports `files`. Concrete implementations
  are constructed only at the composition root (`cmd/server`).
- **Interface at every seam.** `storage.Backend` and the metadata `Repository`
  are interfaces, mocked in tests. Don't reach around them.
- **Soft ~150-line file ceiling.** Crossing it is a signal to split by
  responsibility. No `utils`/`helpers`/`common` grab-bags.
- **Stream, never buffer.** Files flow through `io.Reader`/`io.ReadCloser`;
  never read a whole object into memory. Always `Close()` what you open.
- **Errors wrapped with context** (`fmt.Errorf("...: %w", err)`) and handled
  once. No `panic` on the request path.
- **All schema changes are versioned migrations.** Only the metadata repository
  issues SQL.

## Scope

CairnMark intentionally has **no auth, no multi-tenancy, no UI**. Those belong in
front of it (a gateway / your own auth). Proposals that expand the core surface
will likely be declined; proposals that sharpen the existing concerns are
welcome.
