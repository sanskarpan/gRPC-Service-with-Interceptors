CREATE TABLE IF NOT EXISTS users (
    id TEXT PRIMARY KEY,
    name TEXT NOT NULL CHECK (octet_length(name) BETWEEN 1 AND 4096),
    email TEXT NOT NULL CHECK (octet_length(email) BETWEEN 3 AND 4096),
    age INTEGER NOT NULL CHECK (age >= 0 AND age <= 150),
    created_at TIMESTAMPTZ NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL
);

CREATE INDEX IF NOT EXISTS users_updated_at_idx ON users (updated_at);
