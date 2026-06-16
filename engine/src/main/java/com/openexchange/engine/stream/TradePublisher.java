package com.openexchange.engine.stream;

import com.openexchange.engine.model.Trade;

/**
 * Publishes executed trades to the event stream (Kafka {@code trades} topic), where the gateway's
 * trade tape and the Python risk service consume them.
 *
 * <p>Abstracted behind an interface so the engine's gRPC layer depends on the capability, not on
 * Kafka — tests run with the {@link #NOOP} publisher, no broker required.
 *
 * <p><b>Delivery semantics:</b> publishing happens <i>after</i> the trade is durably recorded in the
 * double-entry ledger, and is <b>best-effort</b>: the ledger (not Kafka) is the source of truth for
 * money, so a broker outage must not fail the order. A failed publish is logged, not retried inline;
 * the durable fix is a transactional outbox (see {@code docs/adr/0003-*}).
 */
public interface TradePublisher {

    /** Publish one executed trade to the stream. Must not throw on broker failure. */
    void publish(Trade trade);

    /** A publisher that emits nothing — for unit tests and broker-less runs. */
    TradePublisher NOOP = trade -> {};
}
