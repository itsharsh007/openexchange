package com.openexchange.engine.stream;

import static org.junit.jupiter.api.Assertions.assertEquals;

import com.openexchange.engine.model.Trade;
import java.time.Duration;
import java.util.HashMap;
import java.util.Map;
import org.apache.kafka.clients.consumer.Consumer;
import org.apache.kafka.clients.consumer.ConsumerConfig;
import org.apache.kafka.clients.consumer.ConsumerRecord;
import org.apache.kafka.clients.consumer.KafkaConsumer;
import org.apache.kafka.common.serialization.ByteArrayDeserializer;
import org.apache.kafka.common.serialization.ByteArraySerializer;
import org.apache.kafka.common.serialization.StringDeserializer;
import org.apache.kafka.common.serialization.StringSerializer;
import org.junit.jupiter.api.AfterEach;
import org.junit.jupiter.api.Test;
import org.junit.jupiter.api.extension.ExtendWith;
import org.springframework.beans.factory.annotation.Autowired;
import org.springframework.kafka.core.DefaultKafkaProducerFactory;
import org.springframework.kafka.core.KafkaTemplate;
import org.springframework.kafka.test.EmbeddedKafkaBroker;
import org.springframework.kafka.test.context.EmbeddedKafka;
import org.springframework.kafka.test.utils.KafkaTestUtils;
import org.springframework.test.context.junit.jupiter.SpringExtension;

/**
 * Proves the engine's trade-publish path end-to-end against a real (in-JVM) Kafka broker: a domain
 * {@link Trade} goes in, the protobuf {@code Trade} comes back off the topic byte-for-byte, keyed by
 * symbol. No containers required, so it runs in CI and locally without Colima.
 */
@ExtendWith(SpringExtension.class)
@EmbeddedKafka(partitions = 1, topics = "trades")
class KafkaTradePublisherTest {

    @Autowired private EmbeddedKafkaBroker broker;

    private KafkaTemplate<String, byte[]> template;
    private Consumer<String, byte[]> consumer;

    private KafkaTemplate<String, byte[]> template() {
        Map<String, Object> props = new HashMap<>();
        props.put("bootstrap.servers", broker.getBrokersAsString());
        props.put("key.serializer", StringSerializer.class);
        props.put("value.serializer", ByteArraySerializer.class);
        return new KafkaTemplate<>(new DefaultKafkaProducerFactory<>(props));
    }

    @AfterEach
    void tearDown() {
        if (consumer != null) consumer.close();
        if (template != null) template.destroy();
    }

    @Test
    void publishesProtoTradeKeyedBySymbol() throws Exception {
        template = template();
        var publisher = new KafkaTradePublisher(template, "trades");

        // A test consumer subscribed before we publish, so it sees the record.
        Map<String, Object> cProps = KafkaTestUtils.consumerProps("test-grp", "true", broker);
        cProps.put(ConsumerConfig.KEY_DESERIALIZER_CLASS_CONFIG, StringDeserializer.class);
        cProps.put(ConsumerConfig.VALUE_DESERIALIZER_CLASS_CONFIG, ByteArrayDeserializer.class);
        cProps.put(ConsumerConfig.AUTO_OFFSET_RESET_CONFIG, "earliest");
        consumer = new KafkaConsumer<>(cProps);
        broker.consumeFromAnEmbeddedTopic(consumer, "trades");

        Trade trade =
                new Trade("AAPL-T1", "AAPL", 15000, 10, "o1", "o2", "alice", "bob", 1_700_000_000_000L);
        publisher.publish(trade);

        ConsumerRecord<String, byte[]> rec =
                KafkaTestUtils.getSingleRecord(consumer, "trades", Duration.ofSeconds(10));

        // Keyed by symbol so all of a symbol's trades stay on one partition, strictly ordered.
        assertEquals("AAPL", rec.key());

        // The value is the shared protobuf Trade contract, round-tripped intact.
        var wire = com.openexchange.proto.Trade.parseFrom(rec.value());
        assertEquals("AAPL-T1", wire.getTradeId());
        assertEquals(15000, wire.getPriceTicks());
        assertEquals(10, wire.getQuantity());
        assertEquals("alice", wire.getBuyAccountId());
        assertEquals("bob", wire.getSellAccountId());
    }
}
