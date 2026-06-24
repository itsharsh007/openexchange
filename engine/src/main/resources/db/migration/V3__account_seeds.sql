-- Opening balance for accounts created via the gateway's signup flow.
--
-- The matching engine's ledger (ledger_entries) tracks money that MOVED through
-- trades. This table records the one-time starting balance that every new account
-- receives on registration — it is NOT a ledger entry (no trade backs it), so it
-- lives separately and is added to the running ledger balance by the gateway's
-- GET /account handler.
--
-- Invariant: every account in `users` should have exactly one row here.
-- The gateway inserts the row immediately after creating the user.

CREATE TABLE account_seeds (
    account_id  TEXT        PRIMARY KEY,
    cash_ticks  BIGINT      NOT NULL DEFAULT 0 CHECK (cash_ticks >= 0),
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);
