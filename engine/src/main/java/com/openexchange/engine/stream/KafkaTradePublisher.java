package com.openexchange.engine.stream;

import com.openexchange.engine.model.Trade;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.kafka.core.KafkaTemplate;
import org.springframework.stereotype.Component;

/**
 * Kafka-backed {@link TradePublisher}. Serializes each trade as the protobuf {@code Trade} message
 * (the same contract every service shares) and sends it to the {@code trades} topic, <b>keyed by
 * symbol</b> so all trades for one symbol land on the same partition and stay strictly ordered.
 *
 * <p>The send is asynchronous and best-effort: we attach a callback that logs failures rather than
 * blocking the order path or throwing. The trade is already durable in the ledger by the time we get
 * here, so a dropped publish costs the trade tape an event, not correctness of the books.
 */
@Component
public class KafkaTradePublisher implements TradePublisher {

    private static final Logger log = LoggerFactory.getLogger(KafkaTradePublisher.class);

    private final KafkaTemplate<String, byte[]> kafka;
    private final String topic;

    public KafkaTradePublisher(
            KafkaTemplate<String, byte[]> kafka,
            @Value("${engine.kafka.trades-topic}") String topic) {
        this.kafka = kafka;
        this.topic = topic;
    }

    @Override
    public void publish(Trade t) {
        byte[] value = toProto(t).toByteArray();
        kafka.send(topic, t.symbol(), value)
                .whenComplete(
                        (result, ex) -> {
                            if (ex != null) {
                                // Best-effort: the ledger already has this trade; just record the gap.
                                log.warn("trade {} not published to '{}': {}", t.tradeId(), topic, ex.toString());
                            }
                        });
    }

    /** Domain {@link Trade} → wire {@link com.openexchange.proto.Trade}. */
    private static com.openexchange.proto.Trade toProto(Trade t) {
        return com.openexchange.proto.Trade.newBuilder()
                .setTradeId(t.tradeId())
                .setSymbol(t.symbol())
                .setPriceTicks(t.priceTicks())
                .setQuantity(t.quantity())
                .setBuyOrderId(t.buyOrderId())
                .setSellOrderId(t.sellOrderId())
                .setTsMillis(t.tsMillis())
                .setBuyAccountId(t.buyAccountId())
                .setSellAccountId(t.sellAccountId())
                .build();
    }
}
