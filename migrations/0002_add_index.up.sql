-- pgfleet:no-transaction
-- 0002 add index (up)
-- The magic comment on line 1 opts this migration out of a transaction, which
-- is required for CREATE INDEX CONCURRENTLY.

create index concurrently if not exists users_created_at_idx on users (created_at);
