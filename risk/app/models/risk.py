"""Per-account risk exposure scoring against configurable limits.

WHAT IT DOES
- Maintains each account's net position per symbol and gross notional exposure, derived from the
  trade stream, and scores how close the account is to its configured limits in [0, 1]
  (0 = no risk, 1 = at or over a limit). Also lists explicit breaches.

WHY RULE-BASED (not ML)
- Risk *limits* are a policy/compliance decision, not a prediction. They must be deterministic,
  auditable, and explainable ("you breached the 10,000-share position cap") — a black-box model is
  the wrong tool. The ML lives in the price/anomaly models; risk exposure is transparent arithmetic.
- All money is in integer **ticks** (matching the proto), so there is no floating-point drift in the
  exposure numbers.

INTEGRATION
- The gateway's reject path can call ``GET /risk/{account_id}`` (or consume the ``risk-signals``
  topic) and block new orders from accounts that are over limit, complementing the per-order anomaly
  check in ``/score-order``.
"""

from __future__ import annotations

from collections import defaultdict, deque
from dataclasses import dataclass, field
from typing import Deque

from app.config import RiskLimits, settings


@dataclass
class AccountState:
    """Running risk state for one account. Updated incrementally from trades/orders (online)."""

    # Net signed position per symbol: +qty when the account bought, -qty when it sold.
    positions: dict[str, int] = field(default_factory=lambda: defaultdict(int))
    # Last trade price per symbol (ticks), to mark positions to market for notional.
    last_price: dict[str, int] = field(default_factory=dict)
    # Order submission timestamps (epoch millis) for the velocity limit, bounded deque.
    order_ts: Deque[int] = field(default_factory=lambda: deque(maxlen=5000))

    def apply_fill(self, symbol: str, signed_qty: int, price_ticks: int) -> None:
        """Apply a fill: positive ``signed_qty`` for a buy, negative for a sell."""
        self.positions[symbol] += signed_qty
        self.last_price[symbol] = price_ticks

    def record_order(self, ts_millis: int) -> None:
        self.order_ts.append(int(ts_millis))

    def gross_notional_ticks(self) -> int:
        """Sum over symbols of |net position| * last price. 'Gross' = absolute exposure, so a long
        and a short do NOT net to zero across symbols (each leg carries risk)."""
        total = 0
        for sym, pos in self.positions.items():
            px = self.last_price.get(sym, 0)
            total += abs(pos) * px
        return int(total)

    def orders_last_minute(self, now_millis: int) -> int:
        cutoff = now_millis - 60_000
        return sum(1 for t in self.order_ts if t >= cutoff)


class RiskScorer:
    """Computes exposure scores + breaches for accounts against configurable limits."""

    def __init__(self, limits: RiskLimits | None = None) -> None:
        self.limits = limits if limits is not None else settings.limits

    def score(self, state: AccountState, *, now_millis: int | None = None) -> tuple[float, list[str], int]:
        """Return ``(exposure_score, breaches, gross_notional_ticks)``.

        The score is the MAX of the per-limit utilisations (fraction of each limit used), clipped to
        [0,1]. Using the max (not the average) means breaching ANY single limit drives the score to
        1.0 — the conservative choice for a risk gate. Each utilisation is what % of that limit the
        account currently uses.
        """
        breaches: list[str] = []
        utilisations: list[float] = []

        # 1) Per-symbol absolute position vs cap.
        max_pos_util = 0.0
        for sym, pos in state.positions.items():
            util = abs(pos) / self.limits.max_position_per_symbol if self.limits.max_position_per_symbol else 0.0
            max_pos_util = max(max_pos_util, util)
            if abs(pos) > self.limits.max_position_per_symbol:
                breaches.append(
                    f"position {abs(pos)} in {sym} exceeds cap {self.limits.max_position_per_symbol}"
                )
        utilisations.append(max_pos_util)

        # 2) Gross notional vs cap.
        gross = state.gross_notional_ticks()
        notional_util = gross / self.limits.max_gross_notional if self.limits.max_gross_notional else 0.0
        utilisations.append(notional_util)
        if gross > self.limits.max_gross_notional:
            breaches.append(
                f"gross notional {gross} ticks exceeds cap {self.limits.max_gross_notional}"
            )

        # 3) Order velocity vs cap (only if we have a clock reference).
        if now_millis is not None:
            opm = state.orders_last_minute(now_millis)
            vel_util = opm / self.limits.max_orders_per_min if self.limits.max_orders_per_min else 0.0
            utilisations.append(vel_util)
            if opm > self.limits.max_orders_per_min:
                breaches.append(
                    f"{opm} orders/min exceeds cap {self.limits.max_orders_per_min} (velocity)"
                )

        exposure_score = float(min(1.0, max(utilisations) if utilisations else 0.0))
        return exposure_score, breaches, gross
