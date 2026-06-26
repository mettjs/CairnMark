-- +goose Up
create table files (
    -- No DB default: the application always supplies a UUIDv7 id (and storage
    -- key). Keeping the generation in Go means the schema has no Postgres-version
    -- dependency, while still getting v7's time-ordered B-tree locality.
    id              uuid primary key,
    storage_key     text not null unique,        -- backend object key (uuidv7)
    filename        text not null,               -- original, user-facing name
    content_type    text not null,
    size_bytes      bigint not null,
    checksum_sha256 text,                         -- hex sha-256
    metadata        jsonb not null default '{}',  -- arbitrary tags/attributes
    created_at      timestamptz not null default now(),
    updated_at      timestamptz,                  -- null until first metadata update
    deleted_at      timestamptz                   -- soft delete
);

create index files_created_at_idx on files (created_at desc);
create index files_checksum_idx   on files (checksum_sha256);
create index files_metadata_gin   on files using gin (metadata);

-- Partial index over soft-deleted rows only: GC's ListDeleted scans just these,
-- and they are a small minority, so the index stays tiny.
create index files_deleted_at_idx on files (deleted_at) where deleted_at is not null;

-- Upload idempotency: maps a client-supplied Idempotency-Key to the file it
-- produced, so a retried POST /files returns the original result instead of
-- creating a duplicate. Rows are expired by GC after a TTL.
create table idempotency_keys (
    key        text primary key,
    file_id    text,                     -- set once status = 'completed'
    status     text not null,            -- 'pending' | 'completed'
    created_at timestamptz not null default now()
);

create index idempotency_keys_created_at_idx on idempotency_keys (created_at);

-- +goose Down
drop table idempotency_keys;
drop table files;
