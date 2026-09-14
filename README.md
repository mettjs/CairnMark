# CairnMark

[![CI](https://github.com/mettjs/cairnmark/actions/workflows/ci.yml/badge.svg)](https://github.com/mettjs/cairnmark/actions/workflows/ci.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-blue.svg)](LICENSE)

> A cairn isn't a pile of stones. It's a pile that means something. Travelers
> stack stones to mark a path, record a place, point the way back. The stones are
> ordinary; the meaning lives in the marking. Strip that away and you have a heap.
> Keep it and every stone is placed, recorded, and findable.

A small, self-hostable file service: an HTTP API in front of **S3-compatible
object storage** with a **Postgres metadata layer**. More than a thin S3 proxy —
it owns a queryable metadata model, inline integrity checks, and tag search —
and deliberately less than a platform: **no auth, no tenancy, no UI** (put it
behind your own gateway).

## Features

- **Streaming upload/download** — multi-GB files flow through bounded memory;
  nothing is buffered whole.
- **Presigned-URL downloads** — `GET` 302-redirects to the object store by
  default, so large transfers never pass through the service.
- **Range requests** — `bytes=` partial downloads.
- **Inline SHA-256** — computed during upload, stored, and verified on read.
- **Queryable metadata** — arbitrary JSONB tags, searchable via a GIN index.
  This is the payoff a plain S3 proxy can't give you.
- **Soft delete + GC** — deletes are instant; objects are purged and orphaned
  objects reclaimed by a background reconciliation sweep.
- **Idempotent uploads** — an optional `Idempotency-Key` header makes retried
  uploads safe: a retry replays the original result instead of duplicating, with
  a crash-safe atomic commit.
- **One backend, many stores** — the storage layer speaks plain S3. RustFS,
  SeaweedFS, and MinIO each ship as a ready-to-run Compose setup; AWS S3 or any
  other S3-compatible store needs only config changes.
- **Prometheus metrics** — per-route request counts and latency plus GC
  reclamation counters, exposed at `/metrics`.

## Quickstart

Requires only Docker. From a clone:

```sh
docker compose up -d --build
```

That starts CairnMark on `:8080` plus Postgres and RustFS — no separate installs.
The service applies its migrations and creates its bucket on boot. Then:

```sh
# Upload a file with tags
curl -i -H "Content-Type: text/plain" \
  --data-binary @README.md \
  "http://localhost:8080/files?filename=README.md&tag.project=cairnmark"

# (grab the "id" from the response, then…)
curl "http://localhost:8080/files/<id>/metadata"
curl -L "http://localhost:8080/files/<id>" -o downloaded.md   # -L follows the presign redirect
curl "http://localhost:8080/files?tag.project=cairnmark"      # search by tag
```

Or run the scripted demo: [`examples/quickstart.sh`](examples/quickstart.sh).

The object store's web console is at
<http://localhost:9001/rustfs/console/> — note the path; the root of `:9001`
answers as the S3 API, not the console.

## Choosing a storage backend

CairnMark is not tied to any object store: it speaks plain S3. Three backends ship
as ready-to-run Compose setups; switching needs no edit to the base file and no
code change — only a different `-f` flag.

**Stop the running stack before switching**, though: Compose won't take the old
substrate down for you (a service parked in an inactive profile isn't an orphan,
so even `--remove-orphans` leaves it running), and it keeps port 9000 bound, so
the incoming store fails to start.

```sh
docker compose down     # add -v to discard the stored objects too
```

| | Default | How to run it | Why you'd pick it |
|---|---|---|---|
| **RustFS** `1.0.0-rc.6` | ✅ | `docker compose up -d --build` | Actively maintained, Apache-2.0, drop-in on port 9000. **Pre-1.0.** |
| **SeaweedFS** `4.46` | | `docker compose -f docker-compose.yml -f compose.seaweedfs.yml up -d --build` | The most mature — Apache-2.0, in production since 2012. Serves S3 on **8333**, not 9000. |
| **MinIO** *(pinned)* | | `docker compose -f docker-compose.yml -f compose.minio.yml up -d --build` | The most familiar, and where your existing data probably is. **Frozen** — see below. |

Each publishes a web UI: RustFS at <http://localhost:9001/rustfs/console/>,
MinIO at <http://localhost:9001>, SeaweedFS's filer browser at
<http://localhost:8888> (objects live under `/buckets/`).

> **Already have data in the old MinIO volume?** Use the MinIO row above. Earlier
> versions of this compose file stored objects in a `miniodata` volume;
> `compose.minio.yml` still declares it, so running that overlay reattaches your
> existing bucket untouched. The RustFS default uses a separate `rustfsdata`
> volume and therefore starts **empty** — your Postgres rows would survive while
> the objects they point at would not be there, giving you `404`s on download.
> Switch deliberately, and migrate the objects first if you want the default.

**The default is chosen for a clean first run, not as a production
recommendation.** All three pass every operation CairnMark performs — round-trip,
ranged reads, unknown-size multipart, presigned GET, listing past the 1000-key
page boundary, and both GC reclamation paths — so this is a choice about
maintenance posture, not capability. For production data, SeaweedFS is the
conservative pick: it is the only one that is neither pre-1.0 nor frozen.

**On MinIO.** It stopped publishing community binaries on 2025-10-23 and pulled
its Docker Hub images, so `minio/minio:latest` no longer resolves and the server
repo is archived. `compose.minio.yml` therefore pins the last community release
from the quay.io mirror, `RELEASE.2025-09-07T16-13-09Z` — a fixed tag on purpose,
since no newer community release is coming. That build still serves the full AGPL
console at the root of `:9001` (unlike RustFS, which serves its console under
`/rustfs/console/`). It also predates the fix for **CVE-2025-62506**,
which requires credentials for a *restricted service or STS account* to exploit;
this stack mints neither, so the precondition doesn't exist as shipped — but
don't issue scoped service-account keys from it. Full reasoning is in
[`compose.minio.yml`](compose.minio.yml).

Note that MinIO the *server* is not `minio-go` the *client library*: the latter is
a separate, actively maintained project, and it is what CairnMark links against
regardless of which store you run.

## Configuration

All configuration is via environment variables (see [`.env.example`](.env.example)).

| Variable | Default | Purpose |
|---|---|---|
| `CAIRNMARK_HTTP_ADDR` | `:8080` | HTTP listen address |
| `CAIRNMARK_SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown grace period |
| `CAIRNMARK_PRESIGN_TTL` | `15m` | Lifetime of presigned download URLs |
| `CAIRNMARK_MAX_UPLOAD_BYTES` | `0` (uncapped) | Max upload body size in bytes; larger uploads get `413` |
| `CAIRNMARK_GC_INTERVAL` | `5m` | Reconciliation sweep cadence (`<=0` disables GC — and all the cleanup below) |
| `CAIRNMARK_GC_GRACE_PERIOD` | `1h` | Min age before an unreferenced object is reclaimed; **must exceed your longest upload** |
| `CAIRNMARK_IDEMPOTENCY_TTL` | `24h` | How long upload idempotency keys are kept before the GC sweep expires them (`<=0` disables expiry) |
| `CAIRNMARK_POSTGRES_DSN` | — *(required)* | pgx connection string |
| `CAIRNMARK_S3_ENDPOINT` | — *(required)* | Object store host:port for service I/O (no scheme) |
| `CAIRNMARK_S3_PUBLIC_ENDPOINT` | = endpoint | Host baked into presigned URLs (see note below) |
| `CAIRNMARK_S3_REGION` | `us-east-1` | S3 region |
| `CAIRNMARK_S3_ACCESS_KEY` | — *(required)* | Access key |
| `CAIRNMARK_S3_SECRET_KEY` | — *(required)* | Secret key |
| `CAIRNMARK_S3_BUCKET` | — *(required)* | Bucket (auto-created if absent) |
| `CAIRNMARK_S3_USE_SSL` | `false` | TLS for the service-side endpoint |
| `CAIRNMARK_S3_PUBLIC_USE_SSL` | = `USE_SSL` | TLS scheme for presigned URLs |

**Port note:** SeaweedFS serves S3 on `8333`; RustFS and MinIO both use `9000`.
The overlay sets both endpoint vars accordingly — `validateEndpoint` accepts any
`host:port`, so nothing else changes.

**Public endpoint:** the service may reach the store by an internal name
(`rustfs:9000` in Compose) that external clients can't resolve. Set
`CAIRNMARK_S3_PUBLIC_ENDPOINT` to a client-reachable host; presigned URLs are
signed against it directly (the host is part of the SigV4 signature and can't be
rewritten afterward).

## API

| Method | Path | Description |
|---|---|---|
| `POST` | `/files` | Upload (raw body). Returns `201` + JSON metadata. Optional `Idempotency-Key` header (see below). |
| `GET` | `/files/{id}` | Download. Default `302` → presigned URL; `Range:` → `206`; `?download=stream` → `200` verified stream. |
| `GET` | `/files/{id}/metadata` | Metadata as JSON. |
| `PATCH` | `/files/{id}/metadata` | Merge tags (or `?mode=replace`). Body is a JSON object. |
| `DELETE` | `/files/{id}` | Soft delete (`204`). Object purged asynchronously. |
| `GET` | `/files` | List/search: `?content_type=`, `?tag.<k>=<v>`, `?limit=`, `?cursor=`. |
| `GET` | `/healthz` · `/readyz` | Liveness / readiness. |
| `GET` | `/metrics` | Prometheus exposition. |

**Upload inputs:** filename from `?filename=` or a `Content-Disposition` header;
content type from `Content-Type` (sniffed when absent); tags from `?tag.<k>=<v>`
query params and/or an `X-Metadata` JSON-object header (for typed/nested values).

**Idempotent uploads:** send an `Idempotency-Key: <unique-string>` header to make
a retried `POST /files` safe. The first request for a key uploads and records the
result; a retry returns the original `201` (with `Idempotency-Replayed: true`)
instead of creating a duplicate. A retry while the first is still in flight gets
`409 Conflict` with a `Retry-After` header saying when to ask again; if the file
the key produced has since been deleted, the retry gets `410 Gone` — switch to a
new key. Keys expire
after `CAIRNMARK_IDEMPOTENCY_TTL` (default 24h). Note: the payload is not
fingerprinted, so reusing a key with different content returns the original
result — keys must be unique per upload.

Example responses:

```jsonc
// POST /files  → 201
{
  "id": "0f8d2312-bf28-42b0-a7a7-0b57a94eba1d",
  "filename": "README.md",
  "content_type": "text/plain",
  "size_bytes": 4096,
  "checksum_sha256": "f0f5…1708",
  "metadata": { "project": "cairnmark" },
  "created_at": "2026-06-24T12:54:54Z",
  "updated_at": null
}

// GET /files?tag.project=cairnmark → 200
{ "files": [ /* …file objects… */ ], "limit": 50, "count": 1 }
```

`updated_at` is `null` until the file's metadata is first changed via `PATCH` —
a quick way to tell an untouched original from an edited record. `limit` defaults
to 50 (max 500) and the response echoes the value actually applied.

**Pagination** is keyset-based: a full page carries a `next_cursor` — pass it
back as `?cursor=` to fetch the files older than it. A page without
`next_cursor` is the last one. Cursors stay accurate under concurrent
uploads/deletes and don't slow down on deep pages the way `OFFSET` does.

## Official SDKs

Hand-written clients for three languages, all exposing the same surface:
streaming uploads/downloads, typed errors, safe retries with idempotency
keys, lazy pagination, and client-side checksum verification (the piece raw
HTTP can't give you — presigned downloads bypass the service, so only the
client can compare the stored SHA-256). Each SDK's README is a complete
integration guide; all require server ≥ v1.1.0.

| Language | Repo | Install |
|---|---|---|
| Go | [`cairnmark-go`](https://github.com/mettjs/cairnmark-go) | `go get github.com/mettjs/cairnmark-go` |
| Python (sync + async) | [`cairnmark-python`](https://github.com/mettjs/cairnmark-python) | `pip install cairnmark` |
| Node.js (TypeScript) | [`cairnmark-node`](https://github.com/mettjs/cairnmark-node) | `npm install cairnmark` |

### Go

```go
package main

import (
	"context"
	"fmt"
	"strings"

	cairnmark "github.com/mettjs/cairnmark-go"
)

func main() {
	ctx := context.Background()
	c, err := cairnmark.New("http://localhost:8080")
	if err != nil {
		panic(err)
	}

	// Upload with a tag; AutoIdempotency makes retries duplicate-safe.
	f, err := c.Upload(ctx, cairnmark.UploadInput{
		Body:            strings.NewReader("hello from Go"),
		Filename:        "hello.txt",
		ContentType:     "text/plain",
		Metadata:        map[string]any{"env": "demo"},
		AutoIdempotency: true,
	})
	if err != nil {
		panic(err)
	}
	fmt.Println("uploaded:", f.ID)

	// Download by id — follows the presign redirect and verifies the SHA-256.
	if _, err := c.DownloadToFile(ctx, f.ID, "hello-copy.txt"); err != nil {
		panic(err)
	}

	// Search by tag, lazily across pages.
	for f, err := range c.ListAll(ctx, cairnmark.ListFilter{Tags: map[string]string{"env": "demo"}}) {
		if err != nil {
			panic(err)
		}
		fmt.Println("found:", f.ID, f.Filename)
	}
}
```

### Python

An `AsyncCairnMark` twin exposes the same surface with `await`.

```python
from cairnmark import CairnMark

with CairnMark("http://localhost:8080") as cm:
    # Upload with a tag; idempotency_key="auto" makes retries duplicate-safe.
    f = cm.upload(
        b"hello from Python",
        filename="hello.txt",
        content_type="text/plain",
        metadata={"env": "demo"},
        idempotency_key="auto",
    )
    print("uploaded:", f.id)

    # Download by id — follows the presign redirect and verifies the SHA-256.
    cm.download_to_file(f.id, "hello-copy.txt")

    # Search by tag, lazily across pages.
    for file in cm.iter_files(tags={"env": "demo"}):
        print("found:", file.id, file.filename)
```

### Node.js

Zero runtime dependencies (global `fetch` + Web Streams, Node 18+), ESM.

```js
import { CairnMark } from "cairnmark";

const cm = new CairnMark("http://localhost:8080");

// Upload with a tag; idempotencyKey: "auto" makes retries duplicate-safe.
const f = await cm.upload("hello from Node", {
  filename: "hello.txt",
  contentType: "text/plain",
  metadata: { env: "demo" },
  idempotencyKey: "auto",
});
console.log("uploaded:", f.id);

// Download by id — follows the presign redirect and verifies the SHA-256.
await cm.downloadToFile(f.id, "hello-copy.txt");

// Search by tag, lazily across pages.
for await (const file of cm.listAll({ tags: { env: "demo" } })) {
  console.log("found:", file.id, file.filename);
}
```

## Architecture

Dependencies point inward; the HTTP layer never touches storage directly.

```
cmd/server        composition root (DI) — the only place concretes are built
internal/api      HTTP handlers + routing (stdlib net/http)
internal/files    service layer: orchestrates storage + metadata, owns the write path
internal/storage  Backend interface  ──  storage/s3 (S3 client, the only backend)
internal/metadata Repository interface ── metadata/postgres (pgx; the only SQL)
internal/gc       background reconciliation: purge soft-deletes, reclaim orphans
internal/metrics  Prometheus metric definitions + the /metrics handler
internal/config   env loading (imported only by cmd/server)
migrations        embedded, versioned SQL (goose)
```

## Development

```sh
go test ./...                    # unit tests (in-memory backend + fake repo)
go test -tags=integration ./...  # against real Postgres + object store (see CONTRIBUTING)
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for conventions and the integration setup.

## License

[MIT](LICENSE) © 2026 Michael Ramirez
