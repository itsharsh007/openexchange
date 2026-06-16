package com.openexchange.engine.model;

/**
 * A single order resting in or passing through the book.
 *
 * <p>Prices are integer <b>ticks</b> (e.g. cents) — never floating point — to avoid rounding bugs
 * in money math. {@code remaining} is mutable: it shrinks as the order is filled.
 */
public final class Order {

    private final String orderId;
    private final String clientOrderId;
    private final String accountId;
    private final String symbol;
    private final Side side;
    private final OrderType type;
    private final long priceTicks; // ignored for MARKET orders
    private final long quantity;
    private final long sequence; // monotonic arrival sequence — drives time priority

    private long remaining;

    public Order(
            String orderId,
            String clientOrderId,
            String accountId,
            String symbol,
            Side side,
            OrderType type,
            long priceTicks,
            long quantity,
            long sequence) {
        if (quantity <= 0) {
            throw new IllegalArgumentException("quantity must be positive: " + quantity);
        }
        if (type == OrderType.LIMIT && priceTicks <= 0) {
            throw new IllegalArgumentException("limit price must be positive: " + priceTicks);
        }
        this.orderId = orderId;
        this.clientOrderId = clientOrderId;
        this.accountId = accountId;
        this.symbol = symbol;
        this.side = side;
        this.type = type;
        this.priceTicks = priceTicks;
        this.quantity = quantity;
        this.sequence = sequence;
        this.remaining = quantity;
    }

    public String orderId() {
        return orderId;
    }

    public String clientOrderId() {
        return clientOrderId;
    }

    public String accountId() {
        return accountId;
    }

    public String symbol() {
        return symbol;
    }

    public Side side() {
        return side;
    }

    public OrderType type() {
        return type;
    }

    public long priceTicks() {
        return priceTicks;
    }

    public long quantity() {
        return quantity;
    }

    public long sequence() {
        return sequence;
    }

    public long remaining() {
        return remaining;
    }

    public long filled() {
        return quantity - remaining;
    }

    public boolean isFilled() {
        return remaining == 0;
    }

    /** Reduce remaining quantity by {@code qty}. */
    public void reduce(long qty) {
        if (qty <= 0 || qty > remaining) {
            throw new IllegalArgumentException("invalid reduce " + qty + " of remaining " + remaining);
        }
        remaining -= qty;
    }

    @Override
    public String toString() {
        return "Order[" + orderId + " " + side + " " + type + " " + remaining + "/" + quantity
                + " @ " + priceTicks + " seq=" + sequence + "]";
    }
}
