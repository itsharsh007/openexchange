package com.openexchange.engine.book;

import com.openexchange.engine.model.OrderStatus;
import com.openexchange.engine.model.Trade;
import java.util.List;

/**
 * Outcome of submitting one order to the book: its resulting status, how much filled, and the
 * trades it generated (possibly several, against multiple resting orders).
 */
public record MatchResult(
        String orderId, OrderStatus status, long filledQuantity, List<Trade> trades) {}
