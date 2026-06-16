package com.openexchange.engine.book;

import com.openexchange.engine.model.Order;
import com.openexchange.engine.model.OrderStatus;
import com.openexchange.engine.model.OrderType;
import com.openexchange.engine.model.Side;
import com.openexchange.engine.model.Trade;
import java.util.ArrayDeque;
import java.util.ArrayList;
import java.util.Comparator;
import java.util.HashMap;
import java.util.List;
import java.util.Map;
import java.util.TreeMap;
import java.util.concurrent.atomic.AtomicLong;
import java.util.function.LongSupplier;

/**
 * A single-symbol limit order book with price-time priority.
 *
 * <p>Each side is a {@link TreeMap} of {@code priceTicks -> FIFO queue of resting orders}: bids
 * ordered descending and asks ascending, so the best price is always {@code firstEntry()}. An
 * {@code orderId -> Order} index gives O(1) cancel lookups. See ADR 0002 for the rationale and
 * Big-O analysis.
 *
 * <p><b>Not thread-safe by design.</b> The engine guarantees a single writer per symbol (one
 * command queue per book), so the matching hot path needs no locks.
 */
public final class OrderBook {

    private final String symbol;
    private final TreeMap<Long, ArrayDeque<Order>> bids = new TreeMap<>(Comparator.reverseOrder());
    private final TreeMap<Long, ArrayDeque<Order>> asks = new TreeMap<>();
    private final Map<String, Order> index = new HashMap<>();
    private final AtomicLong tradeSeq = new AtomicLong();
    private final LongSupplier clock;

    public OrderBook(String symbol) {
        this(symbol, System::currentTimeMillis);
    }

    /** Constructor with an injectable clock for deterministic tests. */
    public OrderBook(String symbol, LongSupplier clock) {
        this.symbol = symbol;
        this.clock = clock;
    }

    public String symbol() {
        return symbol;
    }

    /** Submit an order: match what crosses, rest the remainder (LIMIT only). */
    public MatchResult submit(Order incoming) {
        if (!symbol.equals(incoming.symbol())) {
            throw new IllegalArgumentException(
                    "order symbol " + incoming.symbol() + " != book symbol " + symbol);
        }
        List<Trade> trades = new ArrayList<>();
        TreeMap<Long, ArrayDeque<Order>> opposite = (incoming.side() == Side.BUY) ? asks : bids;

        while (incoming.remaining() > 0 && !opposite.isEmpty()) {
            Map.Entry<Long, ArrayDeque<Order>> best = opposite.firstEntry();
            long bestPrice = best.getKey();
            if (!crosses(incoming, bestPrice)) {
                break; // best price no longer satisfies a limit order — stop
            }
            ArrayDeque<Order> queue = best.getValue();
            while (!queue.isEmpty() && incoming.remaining() > 0) {
                Order resting = queue.peekFirst();
                long qty = Math.min(incoming.remaining(), resting.remaining());
                trades.add(makeTrade(incoming, resting, bestPrice, qty));
                incoming.reduce(qty);
                resting.reduce(qty);
                if (resting.isFilled()) {
                    queue.pollFirst();
                    index.remove(resting.orderId());
                }
            }
            if (queue.isEmpty()) {
                opposite.remove(bestPrice);
            }
        }

        return finalize(incoming, trades);
    }

    /** Cancel a resting order by id. Returns true if it was present and removed. */
    public boolean cancel(String orderId) {
        Order order = index.remove(orderId);
        if (order == null) {
            return false;
        }
        TreeMap<Long, ArrayDeque<Order>> side = (order.side() == Side.BUY) ? bids : asks;
        ArrayDeque<Order> queue = side.get(order.priceTicks());
        if (queue != null) {
            queue.remove(order);
            if (queue.isEmpty()) {
                side.remove(order.priceTicks());
            }
        }
        return true;
    }

    /** True if {@code incoming} is willing to trade against a resting order at {@code restingPrice}. */
    private boolean crosses(Order incoming, long restingPrice) {
        if (incoming.type() == OrderType.MARKET) {
            return true;
        }
        return incoming.side() == Side.BUY
                ? incoming.priceTicks() >= restingPrice
                : incoming.priceTicks() <= restingPrice;
    }

    private Trade makeTrade(Order incoming, Order resting, long priceTicks, long qty) {
        String buyId = incoming.side() == Side.BUY ? incoming.orderId() : resting.orderId();
        String sellId = incoming.side() == Side.BUY ? resting.orderId() : incoming.orderId();
        return new Trade(
                symbol + "-T" + tradeSeq.incrementAndGet(),
                symbol,
                priceTicks,
                qty,
                buyId,
                sellId,
                clock.getAsLong());
    }

    private MatchResult finalize(Order incoming, List<Trade> trades) {
        long filled = incoming.filled();
        if (incoming.isFilled()) {
            return new MatchResult(incoming.orderId(), OrderStatus.FILLED, filled, trades);
        }
        // Unfilled remainder.
        if (incoming.type() == OrderType.MARKET) {
            // No resting of market orders; the remainder is cancelled for lack of liquidity.
            OrderStatus status = filled > 0 ? OrderStatus.PARTIALLY_FILLED : OrderStatus.REJECTED;
            return new MatchResult(incoming.orderId(), status, filled, trades);
        }
        // LIMIT remainder rests in the book.
        rest(incoming);
        OrderStatus status = filled > 0 ? OrderStatus.PARTIALLY_FILLED : OrderStatus.ACCEPTED;
        return new MatchResult(incoming.orderId(), status, filled, trades);
    }

    private void rest(Order order) {
        TreeMap<Long, ArrayDeque<Order>> side = (order.side() == Side.BUY) ? bids : asks;
        side.computeIfAbsent(order.priceTicks(), p -> new ArrayDeque<>()).addLast(order);
        index.put(order.orderId(), order);
    }

    // ── Read-only views (for snapshots / tests) ──────────────────────────────

    public Long bestBid() {
        return bids.isEmpty() ? null : bids.firstKey();
    }

    public Long bestAsk() {
        return asks.isEmpty() ? null : asks.firstKey();
    }

    public boolean contains(String orderId) {
        return index.containsKey(orderId);
    }

    public int restingOrderCount() {
        return index.size();
    }

    /** Aggregated quantity per price level, best first, up to {@code depth} levels per side. */
    public List<long[]> bidLevels(int depth) {
        return levels(bids, depth);
    }

    public List<long[]> askLevels(int depth) {
        return levels(asks, depth);
    }

    private List<long[]> levels(TreeMap<Long, ArrayDeque<Order>> side, int depth) {
        List<long[]> out = new ArrayList<>();
        for (Map.Entry<Long, ArrayDeque<Order>> e : side.entrySet()) {
            if (out.size() >= depth) {
                break;
            }
            long total = 0;
            for (Order o : e.getValue()) {
                total += o.remaining();
            }
            out.add(new long[] {e.getKey(), total});
        }
        return out;
    }
}
