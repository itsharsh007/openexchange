-- Double-entry ledger for the matching engine.
--
-- The engine is authoritative for MATCHING; this ledger is authoritative for MONEY. Every executed
-- trade writes one `trades` row plus a set of balanced `ledger_entries` rows. The core invariant —
-- enforced by construction in the engine and checkable here with a query — is that for any trade,
-- the signed deltas of each asset sum to zero: nothing is created or destroyed.

CREATE TABLE trades (
    trade_id      TEXT PRIMARY KEY,           -- engine-assigned, e.g. AAPL-T1
    symbol        TEXT   NOT NULL,
    price_ticks   BIGINT NOT NULL,            -- execution price in integer ticks (the resting price)
    quantity      BIGINT NOT NULL CHECK (quantity > 0),
    buy_order_id  TEXT   NOT NULL,
    sell_order_id TEXT   NOT NULL,
    ts_millis     BIGINT NOT NULL,            -- engine event time
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE ledger_entries (
    id         BIGSERIAL PRIMARY KEY,
    trade_id   TEXT   NOT NULL REFERENCES trades(trade_id),
    account_id TEXT   NOT NULL,
    asset      TEXT   NOT NULL,               -- 'CASH' (in ticks) or a symbol like 'AAPL' (in shares)
    delta      BIGINT NOT NULL,               -- signed: debit (-) / credit (+)
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_ledger_entries_trade   ON ledger_entries(trade_id);
CREATE INDEX idx_ledger_entries_account ON ledger_entries(account_id, asset);

-- Convenience view: current balance per (account, asset). Balances are DERIVED from the entries,
-- never stored directly — the entries are the single source of truth.
CREATE VIEW account_balances AS
SELECT account_id, asset, SUM(delta) AS balance
FROM ledger_entries
GROUP BY account_id, asset;
