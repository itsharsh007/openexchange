package com.openexchange.engine.grpc;

import static org.junit.jupiter.api.Assertions.assertEquals;
import static org.junit.jupiter.api.Assertions.assertTrue;

import com.openexchange.engine.MatchingEngine;
import com.openexchange.engine.ledger.TradeLedger;
import com.openexchange.engine.stream.TradePublisher;
import com.openexchange.proto.BookRequest;
import com.openexchange.proto.BookSnapshot;
import com.openexchange.proto.CancelOrderRequest;
import com.openexchange.proto.MatchingEngineGrpc;
import com.openexchange.proto.NewOrder;
import com.openexchange.proto.OrderAck;
import com.openexchange.proto.OrderStatus;
import com.openexchange.proto.OrderType;
import com.openexchange.proto.Side;
import io.grpc.ManagedChannel;
import io.grpc.ManagedChannelBuilder;
import io.grpc.Server;
import io.grpc.ServerBuilder;
import java.util.concurrent.TimeUnit;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.BeforeEach;
import org.junit.jupiter.api.Test;

/**
 * Stands up the real gRPC server on an ephemeral port and drives it through a real client channel,
 * so this exercises the full protobuf encode → Netty transport → service → engine path — the same
 * code the Go gateway will hit, minus the network.
 */
class MatchingEngineServiceTest {

    private MatchingEngine engine;
    private Server server;
    private ManagedChannel channel;
    private MatchingEngineGrpc.MatchingEngineBlockingStub stub;

    @BeforeEach
    void setUp() throws Exception {
        engine = new MatchingEngine();
        server =
                ServerBuilder.forPort(0)
                        .addService(
                                new MatchingEngineService(
                                        engine, TradeLedger.NOOP, TradePublisher.NOOP, 10))
                        .build()
                        .start();
        channel = ManagedChannelBuilder.forAddress("localhost", server.getPort()).usePlaintext().build();
        stub = MatchingEngineGrpc.newBlockingStub(channel);
    }

    @AfterEach
    void tearDown() throws Exception {
        channel.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
        server.shutdownNow().awaitTermination(5, TimeUnit.SECONDS);
        engine.shutdown();
    }

    private NewOrder.Builder newOrder(Side side, long price, long qty) {
        return NewOrder.newBuilder()
                .setClientOrderId("c-" + side + "-" + price)
                .setAccountId("acct")
                .setSymbol("AAPL")
                .setSide(side)
                .setType(OrderType.LIMIT)
                .setPriceTicks(price)
                .setQuantity(qty);
    }

    @Test
    void restingBuyThenCrossingSellProducesFill() {
        // A buy with no opposite liquidity rests in the book.
        OrderAck buy = stub.submitOrder(newOrder(Side.BUY, 15000, 10).build());
        assertEquals(OrderStatus.ACCEPTED, buy.getStatus());
        assertEquals(0, buy.getFilledQuantity());

        // The book now shows one bid level.
        BookSnapshot afterBuy = stub.getBook(BookRequest.newBuilder().setSymbol("AAPL").build());
        assertEquals(1, afterBuy.getBidsCount());
        assertEquals(15000, afterBuy.getBids(0).getPriceTicks());
        assertEquals(10, afterBuy.getBids(0).getQuantity());

        // A crossing sell fully fills against the resting buy.
        OrderAck sell = stub.submitOrder(newOrder(Side.SELL, 15000, 10).build());
        assertEquals(OrderStatus.FILLED, sell.getStatus());
        assertEquals(10, sell.getFilledQuantity());

        // Book is empty again.
        BookSnapshot afterFill = stub.getBook(BookRequest.newBuilder().setSymbol("AAPL").build());
        assertEquals(0, afterFill.getBidsCount());
        assertEquals(0, afterFill.getAsksCount());
    }

    @Test
    void cancelRemovesRestingOrder() {
        OrderAck ack = stub.submitOrder(newOrder(Side.BUY, 14000, 5).build());
        assertEquals(OrderStatus.ACCEPTED, ack.getStatus());

        OrderAck cancel =
                stub.cancelOrder(
                        CancelOrderRequest.newBuilder()
                                .setOrderId(ack.getOrderId())
                                .setSymbol("AAPL")
                                .setAccountId("acct")
                                .build());
        assertEquals(OrderStatus.CANCELLED, cancel.getStatus());

        BookSnapshot snap = stub.getBook(BookRequest.newBuilder().setSymbol("AAPL").build());
        assertEquals(0, snap.getBidsCount());

        // Cancelling again is rejected — the order is gone.
        OrderAck again =
                stub.cancelOrder(
                        CancelOrderRequest.newBuilder().setOrderId(ack.getOrderId()).setSymbol("AAPL").build());
        assertEquals(OrderStatus.REJECTED, again.getStatus());
        assertTrue(again.getReason().contains("not found"));
    }

    @Test
    void invalidQuantityIsRejectedAsGrpcError() {
        // quantity <= 0 must surface as an INVALID_ARGUMENT status, not crash the server.
        try {
            stub.submitOrder(newOrder(Side.BUY, 15000, 0).build());
            org.junit.jupiter.api.Assertions.fail("expected INVALID_ARGUMENT");
        } catch (io.grpc.StatusRuntimeException e) {
            assertEquals(io.grpc.Status.Code.INVALID_ARGUMENT, e.getStatus().getCode());
        }
    }
}
