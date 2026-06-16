package com.openexchange.engine.ledger;

import com.openexchange.engine.model.Trade;

/**
 * Persists executed trades to the durable double-entry ledger. Abstracted behind an interface so
 * the matching engine's gRPC layer depends on the capability, not on JDBC — and tests can run with
 * the {@link #NOOP} ledger, no database required.
 */
public interface TradeLedger {

    /** Record one trade and its balanced postings durably and idempotently. */
    void record(Trade trade);

    /** A ledger that persists nothing — for unit tests and DB-less runs. */
    TradeLedger NOOP = trade -> {};
}
