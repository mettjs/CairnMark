# Contributing to CairnMark

Thanks for your interest. CairnMark is a small, deliberately-scoped service; the
bar for changes is that they keep it small and sharp.

## Development setup

```sh
# Bring up Postgres + the object store (and the service, for the full stack):
docker compose up -d postgres rustfs

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

Integration tests run against real Postgres + an object store and are behind a
build tag:

```sh
docker compose up -d postgres rustfs
export CAIRNMARK_POSTGRES_DSN=postgres://cairnmark:cairnmark@localhost:5432/cairnmark?sslmode=disable
export CAIRNMARK_S3_ENDPOINT=localhost:9000 CAIRNMARK_S3_ACCESS_KEY=cairnmark \
       CAIRNMARK_S3_SECRET_KEY=cairnmark-secret CAIRNMARK_S3_BUCKET=cairnmark-it
go test -tags=integration ./...
```

To run them against one of the other supported backends, start that overlay's
substrate instead and point `CAIRNMARK_S3_ENDPOINT` at it — everything else is
identical, since the backend is config-only:

```sh
docker compose -f docker-compose.yml -f compose.minio.yml     up -d postgres minio      # :9000
docker compose -f docker-compose.yml -f compose.seaweedfs.yml up -d postgres seaweedfs  # :8333
```

Note that the integration run uses two buckets: `$CAIRNMARK_S3_BUCKET` and
`$CAIRNMARK_S3_BUCKET-gc`. The GC tests need their own, because a sweep deletes
every object the repository does not claim — sharing a bucket with the storage
tests, which run concurrently, would have each suite deleting the other's data.
Both buckets are created automatically.

CI runs all of the above on every PR, against **all three backends** in a matrix.

## Adding a storage backend

The storage layer speaks plain S3, so a new backend is configuration, not code.
To qualify one:

1. Add `compose.<name>.yml` — copy an existing overlay; each carries comments
   explaining the two non-obvious Compose mechanics (`depends_on` merges, so the
   app service needs `!override`; the unused substrate is parked in a profile).
2. Point the suite at it — this is the qualification, and it is one command:

   ```sh
   docker compose -f docker-compose.yml -f compose.<name>.yml up -d --wait postgres <name>
   export CAIRNMARK_POSTGRES_DSN=postgres://cairnmark:cairnmark@localhost:5432/cairnmark?sslmode=disable
   export CAIRNMARK_S3_ENDPOINT=localhost:<port> CAIRNMARK_S3_ACCESS_KEY=cairnmark \
          CAIRNMARK_S3_SECRET_KEY=cairnmark-secret CAIRNMARK_S3_BUCKET=cairnmark-it
   go test -tags=integration ./...
   ```

   Do not drop the DSN: `internal/metadata/postgres` skips itself with
   `os.Exit(0)` when it is unset, printing no SKIP line — the run still reads as
   fully green while the repository half of the GC contract never executed.

   The suite covers everything CairnMark asks of a store *today*: bucket
   auto-creation, streaming round-trip, ranged reads, unknown-size multipart,
   presigned GETs that are really fetched, listing past the 1000-key page
   boundary, and both GC reclamation paths. One deliberate gap: `PresignPut` is
   on the `storage.Backend` interface but has no caller, so it is not exercised
   on any backend — a green suite says nothing about presigned uploads, and
   wiring them up later means qualifying every backend for them first. Pay attention to
   `TestListReportsUsableLastModified` in particular — a store that reports a
   zero or skewed `LastModified` will have the GC sweep delete live uploads, and
   that is the one failure here that destroys data rather than reporting an error.
3. Add a row to the CI matrix in `.github/workflows/ci.yml` and to the README's
   backend table. A backend that ships as a supported option without a matrix row
   is one that breaks quietly.

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
