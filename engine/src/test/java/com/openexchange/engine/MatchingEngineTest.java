package com.openexchange.engine;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.openexchange.engine.book.MatchResult;
import com.openexchange.engine.model.Order;
import com.openexchange.engine.model.OrderType;
import com.openexchange.engine.model.Side;
import com.openexchange.engine.model.Trade;
import java.util.ArrayList;
import java.util.List;
import java.util.concurrent.CompletableFuture;
import java.util.concurrent.ExecutorService;
import java.util.concurrent.Executors;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.Test;

class MatchingEngineTest {

    private Order order(MatchingEngine eng, String symbol, Side side, long price, long qty) {
        long seq = eng.nextSequence();
        return new Order(
                "o" + seq, "c" + seq, "acct", symbol, side, OrderType.LIMIT, price, qty, seq);
    }

    @Test
    void parallelSubmissionsConserveQuantity() throws Exception {
        MatchingEngine engine = new MatchingEngine();
        int perSide = 2_000;
        long price = 15000;

        // Many client threads hammer the SAME symbol concurrently. The single-writer-per-symbol
        // model must keep the book correct without any locks in OrderBook.
        ExecutorService clients = Executors.newFixedThreadPool(8);
        List<CompletableFuture<MatchResult>> futures = new ArrayList<>();
        try {
            for (int i = 0; i < perSide; i++) {
                futures.add(
                        CompletableFuture.supplyAsync(
                                        () -> engine.submit(order(engine, "AAPL", Side.BUY, price, 1)),
                                        clients)
                                .thenCompose(f -> f));
                futures.add(
                        CompletableFuture.supplyAsync(
                                        () -> engine.submit(order(engine, "AAPL", Side.SELL, price, 1)),
                                        clients)
                                .thenCompose(f -> f));
            }
            CompletableFuture.allOf(futures.toArray(new CompletableFuture[0]))
                    .get(30, TimeUnit.SECONDS);
        } finally {
            clients.shutdown();
        }

        long totalTradedQty = 0;
        for (CompletableFuture<MatchResult> f : futures) {
            for (Trade t : f.get().trades()) {
                totalTradedQty += t.quantity();
            }
        }

        // Equal buy/sell volume at one price => everything matches, nothing lost or duplicated,
        // and the book ends empty. Conservation of quantity is the key safety invariant.
        assertEquals(perSide, totalTradedQty, "every unit of crossing volume must trade exactly once");
        assertEquals(0, engine.book("AAPL").restingOrderCount(), "balanced book must end empty");
        engine.shutdown();
    }

    @Test
    void differentSymbolsRunIndependently() throws Exception {
        MatchingEngine engine = new MatchingEngine();
        MatchResult a = engine.submit(order(engine, "AAPL", Side.BUY, 100, 10)).get();
        MatchResult b = engine.submit(order(engine, "BTC", Side.BUY, 200, 5)).get();
        assertTrue(a.trades().isEmpty() && b.trades().isEmpty());
        assertEquals(1, engine.book("AAPL").restingOrderCount());
        assertEquals(1, engine.book("BTC").restingOrderCount());
        engine.shutdown();
    }
}
