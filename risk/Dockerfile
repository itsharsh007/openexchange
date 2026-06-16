# OpenExchange risk/ML service image.
# python:3.12-slim is small but Debian-based, so confluent-kafka's librdkafka wheel works out of
# the box (manylinux wheels bundle the C lib). We still install build basics defensively in case a
# source build is needed on some arch.

FROM python:3.12-slim AS base

# Twelve-Factor: config comes from the environment at runtime, not baked in.
ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    PIP_NO_CACHE_DIR=1

WORKDIR /app

# Install deps first (separate layer) so code changes don't bust the dependency cache.
COPY requirements.txt .
RUN pip install --upgrade pip && pip install -r requirements.txt

# Copy the application code.
COPY app ./app

# Document the port; uvicorn binds it below.
EXPOSE 8000

# Healthcheck hits the liveness endpoint.
HEALTHCHECK --interval=30s --timeout=3s --start-period=10s --retries=3 \
    CMD python -c "import urllib.request,sys; sys.exit(0 if urllib.request.urlopen('http://localhost:8000/healthz').status==200 else 1)"

# Default command: serve the API. The Kafka consumer starts as a background thread on app startup
# (and self-disables if Kafka is unreachable). To run the consumer as a SEPARATE process instead,
# override the command:  python -m app.kafka_consumer
CMD ["uvicorn", "app.main:app", "--host", "0.0.0.0", "--port", "8000"]
