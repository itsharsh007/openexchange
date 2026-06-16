package com.openexchange.engine.grpc;

import com.openexchange.engine.MatchingEngine;
import com.openexchange.engine.book.MatchResult;
import com.openexchange.engine.book.OrderBook;
import com.openexchange.engine.ledger.TradeLedger;
import com.openexchange.engine.model.Order;
import com.openexchange.engine.model.Trade;
import com.openexchange.proto.BookRequest;
import com.openexchange.proto.BookSnapshot;
import com.openexchange.proto.CancelOrderRequest;
import com.openexchange.proto.MatchingEngineGrpc;
import com.openexchange.proto.NewOrder;
import com.openexchange.proto.OrderAck;
import com.openexchange.proto.PriceLevel;
import io.grpc.Status;
import io.grpc.stub.StreamObserver;
import java.util.List;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.stereotype.Component;

/**
 * gRPC front door to the matching engine: translates protobuf requests into domain commands, runs
 * them on the engine, and translates the result back. It owns no state — all order-book state and
 * the single-writer concurrency guarantee live in {@link MatchingEngine}.
 *
 * <p>The proto package ({@code com.openexchange.proto}) and the domain package
 * ({@code com.openexchange.engine.model}) both define {@code Side}, {@code OrderType}, and
 * {@code OrderStatus}. To keep the mapping readable we import the proto messages and fully-qualify
 * the few clashing domain enums.
 */
@Component
public class MatchingEngineService extends MatchingEngineGrpc.MatchingEngineImplBase {

    private static final Logger log = LoggerFactory.getLogger(MatchingEngineService.class);

    private final MatchingEngine engine;
    private final TradeLedger ledger;
    private final int defaultDepth;

    public MatchingEngineService(
            MatchingEngine engine,
            TradeLedger ledger,
            @Value("${engine.book.default-depth}") int defaultDepth) {
        this.engine = engine;
        this.ledger = ledger;
        this.defaultDepth = defaultDepth;
    }

    @Override
    public void submitOrder(NewOrder req, StreamObserver<OrderAck> responseObserver) {
        try {
            // The engine assigns the authoritative order id and arrival sequence; the client only
            // supplies its idempotency key (client_order_id).
            long seq = engine.nextSequence();
            Order order =
                    new Order(
                            "o" + seq,
                            req.getClientOrderId(),
                            req.getAccountId(),
                            req.getSymbol(),
                            toDomainSide(req.getSide()),
                            toDomainType(req.getType()),
                            req.getPriceTicks(),
                            req.getQuantity(),
                            seq);

            // .get() blocks this RPC thread until the symbol's single writer has processed the order.
            MatchResult result = engine.submit(order).get();

            // Persist the money side BEFORE acking the client: a "filled" response must mean the
            // double-entry ledger already recorded it durably. Each record() is its own transaction
            // and idempotent on trade_id.
            for (Trade trade : result.trades()) {
                ledger.record(trade);
            }
            // TODO(phase-1): also publish result.trades() to the Kafka `trades` topic here.

            responseObserver.onNext(
                    OrderAck.newBuilder()
                            .setOrderId(result.orderId())
                            .setStatus(toProtoStatus(result.status()))
                            .setFilledQuantity(result.filledQuantity())
                            .build());
            responseObserver.onCompleted();
        } catch (IllegalArgumentException e) {
            // Bad input (non-positive qty/price, etc.) — reject rather than crash the stream.
            responseObserver.onError(
                    Status.INVALID_ARGUMENT.withDescription(e.getMessage()).asRuntimeException());
        } catch (Exception e) {
            log.error("submitOrder failed", e);
            responseObserver.onError(
                    Status.INTERNAL.withDescription("engine error").withCause(e).asRuntimeException());
        }
    }

    @Override
    public void cancelOrder(CancelOrderRequest req, StreamObserver<OrderAck> responseObserver) {
        try {
            boolean removed = engine.cancel(req.getSymbol(), req.getOrderId()).get();
            OrderAck.Builder ack = OrderAck.newBuilder().setOrderId(req.getOrderId());
            if (removed) {
                ack.setStatus(com.openexchange.proto.OrderStatus.CANCELLED);
            } else {
                ack.setStatus(com.openexchange.proto.OrderStatus.REJECTED)
                        .setReason("order not found or already filled");
            }
            responseObserver.onNext(ack.build());
            responseObserver.onCompleted();
        } catch (Exception e) {
            log.error("cancelOrder failed", e);
            responseObserver.onError(
                    Status.INTERNAL.withDescription("engine error").withCause(e).asRuntimeException());
        }
    }

    @Override
    public void getBook(BookRequest req, StreamObserver<BookSnapshot> responseObserver) {
        try {
            int depth = req.getDepth() > 0 ? req.getDepth() : defaultDepth;
            OrderBook book = engine.book(req.getSymbol());
            BookSnapshot.Builder snap =
                    BookSnapshot.newBuilder().setSymbol(req.getSymbol()).setTsMillis(nowMillis());
            for (long[] level : book.bidLevels(depth)) {
                snap.addBids(level(level));
            }
            for (long[] level : book.askLevels(depth)) {
                snap.addAsks(level(level));
            }
            responseObserver.onNext(snap.build());
            responseObserver.onCompleted();
        } catch (Exception e) {
            log.error("getBook failed", e);
            responseObserver.onError(
                    Status.INTERNAL.withDescription("engine error").withCause(e).asRuntimeException());
        }
    }

    // ── mapping helpers ──────────────────────────────────────────────────────

    private static PriceLevel level(long[] priceAndQty) {
        return PriceLevel.newBuilder()
                .setPriceTicks(priceAndQty[0])
                .setQuantity(priceAndQty[1])
                .build();
    }

    private static com.openexchange.engine.model.Side toDomainSide(com.openexchange.proto.Side s) {
        return s == com.openexchange.proto.Side.SELL
                ? com.openexchange.engine.model.Side.SELL
                : com.openexchange.engine.model.Side.BUY;
    }

    private static com.openexchange.engine.model.OrderType toDomainType(
            com.openexchange.proto.OrderType t) {
        return t == com.openexchange.proto.OrderType.MARKET
                ? com.openexchange.engine.model.OrderType.MARKET
                : com.openexchange.engine.model.OrderType.LIMIT;
    }

    private static com.openexchange.proto.OrderStatus toProtoStatus(
            com.openexchange.engine.model.OrderStatus status) {
        return switch (status) {
            case ACCEPTED -> com.openexchange.proto.OrderStatus.ACCEPTED;
            case PARTIALLY_FILLED -> com.openexchange.proto.OrderStatus.PARTIALLY_FILLED;
            case FILLED -> com.openexchange.proto.OrderStatus.FILLED;
            case CANCELLED -> com.openexchange.proto.OrderStatus.CANCELLED;
            case REJECTED -> com.openexchange.proto.OrderStatus.REJECTED;
        };
    }

    private static long nowMillis() {
        return System.currentTimeMillis();
    }
}
