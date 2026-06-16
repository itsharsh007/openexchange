package com.openexchange.engine.model;

/** Lifecycle status of an order. */
public enum OrderStatus {
    ACCEPTED,
    PARTIALLY_FILLED,
    FILLED,
    CANCELLED,
    REJECTED
}
