"""In-memory feature store shared by the consumer and the API.

WHY in-memory (for now)
- The architecture calls for a Postgres-backed feature store, but for the service to be runnable and
  testable with zero infra we keep an in-process store with the SAME interface a DB-backed one would
  expose (``apply_trade``, ``apply_order``, getters). Swapping to Postgres later is a drop-in.
- Online updates: every trade/order mutates O(1) state, so features are always fresh ("online"
  learning of statistics, even though the ML models themselves are trained in batch).

THREADING
- The Kafka consumer runs in a background thread and writes here while the API reads. Mutations are
  small and we guard with a lock. For this simulation that's plenty; a real system would shard by
  symbol/account or use a concurrent structure.
"""

from __future__ import annotations

import threading
from collections import defaultdict, deque
from typing import Deque

from app.features import AccountOrderStats
from app.models.risk import AccountState

# How many recent trade prices to retain per symbol for the price model's window.
PRICE_HISTORY = 200


class FeatureStore:
    def __init__(self) -> None:
        self._lock = threading.Lock()
        self._prices: dict[str, Deque[int]] = defaultdict(lambda: deque(maxlen=PRICE_HISTORY))
        self._buy_vol: dict[str, float] = defaultdict(float)
        self._sell_vol: dict[str, float] = defaultdict(float)
        self._order_stats: dict[str, AccountOrderStats] = defaultdict(AccountOrderStats)
        self._account_state: dict[str, AccountState] = defaultdict(AccountState)

    # ── Writers (called by the consumer or tests) ──────────────────────────────
    def apply_trade(
        self,
        *,
        symbol: str,
        price_ticks: int,
        quantity: int,
        buy_account_id: str | None = None,
        sell_account_id: str | None = None,
    ) -> None:
        """Update price history, volumes, and both accounts' positions from a trade."""
        with self._lock:
            self._prices[symbol].append(int(price_ticks))
            # Without aggressor info we split the volume; the imbalance feature is a coarse proxy.
            self._buy_vol[symbol] += quantity
            self._sell_vol[symbol] += quantity
            self._order_stats_mid_update(symbol, price_ticks)
            if buy_account_id:
                self._account_state[buy_account_id].apply_fill(symbol, +int(quantity), int(price_ticks))
            if sell_account_id:
                self._account_state[sell_account_id].apply_fill(symbol, -int(quantity), int(price_ticks))

    def apply_order(
        self,
        *,
        account_id: str,
        symbol: str,
        quantity: int,
        ts_millis: int,
        is_cancel: bool = False,
    ) -> None:
        """Update per-account order-pattern stats (size, arrival, cancels) and velocity clock."""
        with self._lock:
            stats = self._order_stats[account_id]
            if is_cancel:
                stats.record_cancel()
            else:
                stats.record_submit(quantity, ts_millis)
                self._account_state[account_id].record_order(ts_millis)

    def _order_stats_mid_update(self, symbol: str, price_ticks: int) -> None:
        # Track last mid per symbol across all accounts' stats lazily via a shared dict key.
        # We store under a per-symbol key so anomaly scoring can read a recent reference price.
        for stats in self._order_stats.values():
            stats.last_mid_by_symbol[symbol] = float(price_ticks)
            stats.last_mid_by_symbol["_"] = float(price_ticks)

    # ── Readers (called by the API) ────────────────────────────────────────────
    def recent_prices(self, symbol: str) -> list[int]:
        with self._lock:
            return list(self._prices.get(symbol, deque()))

    def volumes(self, symbol: str) -> tuple[float, float]:
        with self._lock:
            return self._buy_vol.get(symbol, 0.0), self._sell_vol.get(symbol, 0.0)

    def recent_mid(self, symbol: str) -> int | None:
        with self._lock:
            dq = self._prices.get(symbol)
            return int(dq[-1]) if dq else None

    def order_stats(self, account_id: str) -> AccountOrderStats:
        with self._lock:
            return self._order_stats[account_id]

    def account_state(self, account_id: str) -> AccountState:
        with self._lock:
            return self._account_state[account_id]


# Process-wide singleton. Pure in-memory construction => import-safe (no network).
store = FeatureStore()
