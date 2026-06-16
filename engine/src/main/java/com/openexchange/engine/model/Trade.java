package com.openexchange.engine.model;

/**
 * An executed trade between a resting (maker) order and an incoming (taker) order.
 *
 * <p>By convention the trade executes at the <b>resting order's price</b> (price improvement
 * accrues to the taker), and price is in integer ticks.
 */
public record Trade(
        String tradeId,
        String symbol,
        long priceTicks,
        long quantity,
        String buyOrderId,
        String sellOrderId,
        long tsMillis) {}
