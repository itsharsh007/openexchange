package com.openexchange.engine;

import com.openexchange.engine.book.MatchResult;
import com.openexchange.engine.book.OrderBook;
import com.openexchange.engine.model.Order;
import java.util.Map;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ConcurrentHashMap;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.atomic.AtomicLong;

/**
 * Multi-symbol matching engine implementing the <b>single-writer-per-symbol</b> concurrency model.
 *
 * <p>Each symbol owns one {@link OrderBook} and one single-threaded executor. All commands for a
 * symbol are funneled through that one thread, so a book is never touched by two threads at once —
 * the matching hot path needs <b>no locks</b>, yet different symbols run fully in parallel. This is
 * the same idea behind the LMAX Disruptor: serialize per partition, parallelize across partitions.
 *
 * <p>To scale beyond one machine, shard symbols across engine instances (symbol -> instance via
 * consistent hashing); each symbol's book then lives on exactly one instance, preserving ordering
 * without distributed locks.
 */
public final class MatchingEngine {

    /** A symbol's book plus its dedicated single writer thread. */
    private record SymbolActor(OrderBook book, ExecutorService writer) {}

    private final Map<String, SymbolActor> actors = new ConcurrentHashMap<>();
    private final AtomicLong sequence = new AtomicLong();

    /** Submit an order; matching runs on the symbol's single writer thread. */
    public CompletableFuture<MatchResult> submit(Order order) {
        SymbolActor actor = actorFor(order.symbol());
        return CompletableFuture.supplyAsync(() -> actor.book().submit(order), actor.writer());
    }

    /** Cancel a resting order on its symbol's writer thread. */
    public CompletableFuture<Boolean> cancel(String symbol, String orderId) {
        SymbolActor actor = actorFor(symbol);
        return CompletableFuture.supplyAsync(() -> actor.book().cancel(orderId), actor.writer());
    }

    /** Monotonic, thread-safe arrival sequence — callers stamp orders with this for time priority. */
    public long nextSequence() {
        return sequence.incrementAndGet();
    }

    /** Direct (synchronous) access to a book for snapshots/reads. Safe for read-only queries. */
    public OrderBook book(String symbol) {
        return actorFor(symbol).book();
    }

    private SymbolActor actorFor(String symbol) {
        return actors.computeIfAbsent(
                symbol,
                s ->
                        new SymbolActor(
                                new OrderBook(s),
                                Executors.newSingleThreadExecutor(
                                        r -> {
                                            Thread t = new Thread(r, "engine-" + s);
                                            t.setDaemon(true);
                                            return t;
                                        })));
    }

    /** Gracefully stop all writer threads. */
    public void shutdown() {
        actors.values().forEach(a -> a.writer().shutdown());
    }
}
