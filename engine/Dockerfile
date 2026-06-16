# Multi-stage build. IMPORTANT: build with the repo root as context, not engine/, because the
# Gradle build reads the shared contract at proto/ (the single source of truth):
#   docker build -f engine/Dockerfile -t openexchange-engine .

# ── build stage ──────────────────────────────────────────────────────────────
FROM gradle:8.10-jdk17 AS build
WORKDIR /src
# Copy the contract first so a proto-only change invalidates fewer layers than a source change.
COPY proto/ proto/
COPY engine/ engine/
WORKDIR /src/engine
# bootJar produces the runnable fat jar; --no-daemon keeps the container build deterministic.
RUN gradle --no-daemon bootJar

# ── runtime stage ────────────────────────────────────────────────────────────
FROM eclipse-temurin:17-jre AS runtime
WORKDIR /app
COPY --from=build /src/engine/build/libs/engine-*.jar app.jar
# gRPC order API + HTTP actuator health (ports are env-overridable; see application.yml).
EXPOSE 50051 8081
ENTRYPOINT ["java", "-jar", "app.jar"]
