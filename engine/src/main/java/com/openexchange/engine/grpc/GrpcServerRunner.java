package com.openexchange.engine.grpc;

import io.grpc.Server;
import io.grpc.ServerBuilder;
import java.io.IOException;
import java.util.concurrent.TimeUnit;
import org.slf4j.Logger;
import org.slf4j.LoggerFactory;
import org.springframework.beans.factory.annotation.Value;
import org.springframework.context.SmartLifecycle;
import org.springframework.stereotype.Component;

/**
 * Starts and stops the gRPC server in step with the Spring context.
 *
 * <p>Implementing {@link SmartLifecycle} (rather than starting a server in {@code main}) means
 * Spring brings the gRPC port up only after every bean — including the {@link
 * com.openexchange.engine.MatchingEngine} — is fully constructed, and shuts it down gracefully on
 * {@code SIGTERM}, draining in-flight RPCs before killing the engine's writer threads. That clean
 * lifecycle is what lets Kubernetes roll the pod without dropping orders.
 */
@Component
public class GrpcServerRunner implements SmartLifecycle {

    private static final Logger log = LoggerFactory.getLogger(GrpcServerRunner.class);

    private final MatchingEngineService service;
    private final int port;
    private Server server;
    private volatile boolean running = false;

    public GrpcServerRunner(MatchingEngineService service, @Value("${engine.grpc.port}") int port) {
        this.service = service;
        this.port = port;
    }

    @Override
    public void start() {
        try {
            server = ServerBuilder.forPort(port).addService(service).build().start();
            running = true;
            log.info("gRPC matching-engine server listening on port {}", port);
        } catch (IOException e) {
            throw new IllegalStateException("failed to start gRPC server on port " + port, e);
        }
    }

    @Override
    public void stop() {
        if (server != null) {
            // Stop accepting new RPCs, let in-flight ones finish, then force-terminate after a grace period.
            server.shutdown();
            try {
                if (!server.awaitTermination(10, TimeUnit.SECONDS)) {
                    server.shutdownNow();
                }
            } catch (InterruptedException e) {
                Thread.currentThread().interrupt();
                server.shutdownNow();
            }
            log.info("gRPC matching-engine server stopped");
        }
        running = false;
    }

    @Override
    public boolean isRunning() {
        return running;
    }
}
