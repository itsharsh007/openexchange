"""OpenExchange risk / ML service package.

This package is intentionally import-safe with NO running Kafka or Postgres: importing any module
here must never open a socket. All external connections are created lazily (see
``app.kafka_consumer`` and ``app.config``). This keeps ``py_compile`` / ``pytest`` / CI green
without infrastructure, and lets the FastAPI app boot even when the backbone is down.
"""

__version__ = "0.1.0"
