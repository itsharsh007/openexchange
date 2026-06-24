package com.openexchange.engine.book;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertFalse;
import static org.junit.jupiter.api.Assertions.assertNull;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.openexchange.engine.model.Order;
import com.openexchange.engine.model.OrderStatus;
import com.openexchange.engine.model.OrderType;
import com.openexchange.engine.model.Side;
import com.openexchange.engine.model.Trade;
import java.util.concurrent.atomic.AtomicLong;
import org.junit.jupiter.api.Test;

class OrderBookTest {

    private final AtomicLong seq = new AtomicLong();

    private OrderBook newBook() {
        return new OrderBook("AAPL", () -> 1_000L); // fixed clock
    }

    // Default: each order gets its own account, so unrelated orders cross normally. Tests that
    // exercise self-match prevention pass an explicit shared account via the overload below.
    private Order order(String id, Side side, OrderType type, long price, long qty) {
        return order(id, "acct-" + id, side, type, price, qty);
    }

    private Order order(String id, String account, Side side, OrderType type, long price, long qty) {
        return new Order(id, "c-" + id, account, "AAPL", side, type, price, qty, seq.incrementAndGet());
    }

    @Test
    void restingOrderHasNoTradesAndSitsOnBook() {
        OrderBook book = newBook();
        MatchResult r = book.submit(order("o1", Side.BUY, OrderType.LIMIT, 15000, 100));

        assertEquals(OrderStatus.ACCEPTED, r.status());
        assertEquals(0, r.filledQuantity());
        assertTrue(r.trades().isEmpty());
        assertEquals(15000L, book.bestBid());
        assertNull(book.bestAsk());
        assertTrue(book.contains("o1"));
    }

    @Test
    void crossingOrderMatchesAtRestingPrice() {
        OrderBook book = newBook();
        book.submit(order("buy", Side.BUY, OrderType.LIMIT, 15025, 100)); // resting bid
        MatchResult r = book.submit(order("sell", Side.SELL, OrderType.LIMIT, 15000, 100));

        assertEquals(OrderStatus.FILLED, r.status());
        assertEquals(100, r.filledQuantity());
        assertEquals(1, r.trades().size());
        Trade t = r.trades().get(0);
        assertEquals(15025L, t.priceTicks()); // executes at resting (maker) price
        assertEquals(100L, t.quantity());
        assertEquals("buy", t.buyOrderId());
        assertEquals("sell", t.sellOrderId());
        // book is now empty
        assertNull(book.bestBid());
        assertNull(book.bestAsk());
        assertEquals(0, book.restingOrderCount());
    }

    @Test
    void partialFillRestsRemainder() {
        OrderBook book = newBook();
        book.submit(order("buy", Side.BUY, OrderType.LIMIT, 15000, 100));
        MatchResult r = book.submit(order("sell", Side.SELL, OrderType.LIMIT, 15000, 150));

        assertEquals(OrderStatus.PARTIALLY_FILLED, r.status());
        assertEquals(100, r.filledQuantity());
        // 50 remaining sells rest on the ask side
        assertEquals(15000L, book.bestAsk());
        assertNull(book.bestBid());
    }

    @Test
    void priceTimePriorityIsFifoWithinLevel() {
        OrderBook book = newBook();
        book.submit(order("b1", Side.BUY, OrderType.LIMIT, 15000, 50)); // first in
        book.submit(order("b2", Side.BUY, OrderType.LIMIT, 15000, 50)); // second in
        MatchResult r = book.submit(order("s1", Side.SELL, OrderType.LIMIT, 15000, 50));

        assertEquals(1, r.trades().size());
        // earliest resting bid (b1) must fill first
        assertEquals("b1", r.trades().get(0).buyOrderId());
        assertTrue(book.contains("b2"));
        assertFalse(book.contains("b1"));
    }

    @Test
    void bestPriceFillsBeforeWorse() {
        OrderBook book = newBook();
        book.submit(order("ask_hi", Side.SELL, OrderType.LIMIT, 15010, 100));
        book.submit(order("ask_lo", Side.SELL, OrderType.LIMIT, 15000, 100)); // better ask
        MatchResult r = book.submit(order("buy", Side.BUY, OrderType.LIMIT, 15010, 100));

        assertEquals(OrderStatus.FILLED, r.status());
        // should hit the cheaper ask first
        assertEquals(15000L, r.trades().get(0).priceTicks());
        assertEquals("ask_lo", r.trades().get(0).sellOrderId());
    }

    @Test
    void marketOrderSweepsMultipleLevels() {
        OrderBook book = newBook();
        book.submit(order("a1", Side.SELL, OrderType.LIMIT, 15000, 40));
        book.submit(order("a2", Side.SELL, OrderType.LIMIT, 15010, 40));
        MatchResult r = book.submit(order("m", Side.BUY, OrderType.MARKET, 0, 100));

        assertEquals(OrderStatus.PARTIALLY_FILLED, r.status()); // only 80 available
        assertEquals(80, r.filledQuantity());
        assertEquals(2, r.trades().size());
        assertEquals(15000L, r.trades().get(0).priceTicks());
        assertEquals(15010L, r.trades().get(1).priceTicks());
    }

    @Test
    void marketOrderWithNoLiquidityIsRejected() {
        OrderBook book = newBook();
        MatchResult r = book.submit(order("m", Side.BUY, OrderType.MARKET, 0, 100));
        assertEquals(OrderStatus.REJECTED, r.status());
        assertEquals(0, r.filledQuantity());
    }

    @Test
    void cancelRemovesRestingOrder() {
        OrderBook book = newBook();
        book.submit(order("o1", Side.BUY, OrderType.LIMIT, 15000, 100));
        assertTrue(book.cancel("o1"));
        assertFalse(book.contains("o1"));
        assertNull(book.bestBid());
        assertFalse(book.cancel("o1")); // second cancel is a no-op
    }

    @Test
    void selfMatchIsPreventedAndBothOrdersRest() {
        OrderBook book = newBook();
        book.submit(order("mine_buy", "acctA", Side.BUY, OrderType.LIMIT, 15000, 100));
        // Same account, crossing price — must NOT trade with itself.
        MatchResult r = book.submit(order("mine_sell", "acctA", Side.SELL, OrderType.LIMIT, 15000, 100));

        assertEquals(OrderStatus.ACCEPTED, r.status());
        assertTrue(r.trades().isEmpty());
        // Both orders remain resting (a same-account locked book is fine: they never match).
        assertTrue(book.contains("mine_buy"));
        assertTrue(book.contains("mine_sell"));
        assertEquals(15000L, book.bestBid());
        assertEquals(15000L, book.bestAsk());
    }

    @Test
    void skipsOwnOrderButFillsOtherAccountAtSameLevel() {
        OrderBook book = newBook();
        book.submit(order("mine_buy", "acctA", Side.BUY, OrderType.LIMIT, 15000, 100)); // ahead in FIFO
        book.submit(order("other_buy", "acctB", Side.BUY, OrderType.LIMIT, 15000, 100));
        MatchResult r = book.submit(order("mine_sell", "acctA", Side.SELL, OrderType.LIMIT, 15000, 100));

        assertEquals(OrderStatus.FILLED, r.status());
        assertEquals(1, r.trades().size());
        // Skipped our own (earlier) bid, matched the other account's bid behind it.
        assertEquals("other_buy", r.trades().get(0).buyOrderId());
        assertFalse(book.contains("other_buy"));
        // Our own bid is left untouched and still resting at the front of the level.
        assertTrue(book.contains("mine_buy"));
        assertEquals(15000L, book.bestBid());
    }

    @Test
    void marketOrderCannotSweepOwnLiquidity() {
        OrderBook book = newBook();
        book.submit(order("mine_ask", "acctA", Side.SELL, OrderType.LIMIT, 15000, 100));
        MatchResult r = book.submit(order("mine_mkt", "acctA", Side.BUY, OrderType.MARKET, 0, 100));

        assertEquals(OrderStatus.REJECTED, r.status()); // no fillable (other-account) liquidity
        assertEquals(0, r.filledQuantity());
        assertTrue(book.contains("mine_ask")); // own resting ask untouched
    }

    @Test
    void nonCrossingLimitDoesNotMatch() {
        OrderBook book = newBook();
        book.submit(order("buy", Side.BUY, OrderType.LIMIT, 14900, 100));
        MatchResult r = book.submit(order("sell", Side.SELL, OrderType.LIMIT, 15000, 100));

        assertEquals(OrderStatus.ACCEPTED, r.status());
        assertTrue(r.trades().isEmpty());
        assertEquals(14900L, book.bestBid());
        assertEquals(15000L, book.bestAsk());
    }
}
