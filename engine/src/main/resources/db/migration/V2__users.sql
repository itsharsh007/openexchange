-- Registered users for password-backed auth (issued + verified by the Go gateway).
--
-- The engine owns the database schema via Flyway, so the users table is created
-- here even though the gateway is what reads and writes it. account_id is the
-- stable identity used everywhere downstream (orders, ledger, JWT subject); email
-- is the login handle and is stored normalized (lower-cased) so it's unique
-- case-insensitively. Passwords are never stored — only their bcrypt hash.

CREATE TABLE users (
    account_id    TEXT PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
