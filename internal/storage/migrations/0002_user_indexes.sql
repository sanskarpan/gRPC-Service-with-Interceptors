-- 0002_user_indexes.sql
-- Additional lookup and ordering indexes that are independent of the initial
-- schema. Idempotent so re-running the migration is safe.
CREATE INDEX IF NOT EXISTS users_email_idx ON users (email);
CREATE INDEX IF NOT EXISTS users_created_at_idx ON users (created_at DESC);
