package com.openexchange.engine;

import org.springframework.boot.SpringApplication;
import org.springframework.boot.autoconfigure.SpringBootApplication;
import org.springframework.context.annotation.Bean;

/**
 * Boots the matching-engine service.
 *
 * <p>This process exposes two ports: a gRPC server (the order API the Go gateway calls) and an HTTP
 * port serving Spring Actuator's health endpoint for container/orchestrator probes. The actual
 * matching logic lives in {@link MatchingEngine}, which is registered as a singleton bean so the
 * gRPC layer and (later) the Kafka publisher share one engine instance.
 */
@SpringBootApplication
public class EngineApplication {

    public static void main(String[] args) {
        SpringApplication.run(EngineApplication.class, args);
    }

    /** One engine for the whole process; all symbols and their single-writer threads live here. */
    @Bean
    public MatchingEngine matchingEngine() {
        return new MatchingEngine();
    }
}
