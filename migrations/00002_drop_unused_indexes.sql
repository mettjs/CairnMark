-- +goose Up
-- Neither index serves any query the service issues:
--   * List orders by id desc (UUIDv7 ≈ creation order), served by the PK —
--     files_created_at_idx is never consulted.
--   * Nothing looks files up by checksum yet; files_checksum_idx only earns
--     its keep if content-addressed dedup lands (recreate it then).
-- Both cost a write on every upload, so drop them until a query needs them.
drop index files_created_at_idx;
drop index files_checksum_idx;

-- +goose Down
create index files_created_at_idx on files (created_at desc);
create index files_checksum_idx   on files (checksum_sha256);
