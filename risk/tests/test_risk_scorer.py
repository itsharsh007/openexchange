"""Tests for the per-account risk exposure scorer. Pure logic, no Kafka/DB/network.

These use only stdlib + the project code (no sklearn), so they run on a bare box.
"""

from __future__ import annotations

from app.config import RiskLimits
from app.models.risk import AccountState, RiskScorer


def make_scorer() -> RiskScorer:
    # Small, explicit limits so the arithmetic in assertions is obvious.
    return RiskScorer(
        RiskLimits(
            max_position_per_symbol=100,
            max_gross_notional=1_000_000,
            max_orders_per_min=10,
        )
    )


def test_no_activity_is_zero_risk():
    scorer = make_scorer()
    state = AccountState()
    score, breaches, gross = scorer.score(state)
    assert score == 0.0
    assert breaches == []
    assert gross == 0


def test_position_under_limit_scales_linearly():
    scorer = make_scorer()
    state = AccountState()
    # Net +50 shares of ABC at 100 ticks => 50/100 position utilisation = 0.5.
    state.apply_fill("ABC", +50, 100)
    score, breaches, _ = scorer.score(state)
    assert breaches == []
    # gross notional = 50*100 = 5000, util = 5000/1_000_000 = 0.005 -> position util (0.5) dominates.
    assert abs(score - 0.5) < 1e-9


def test_position_breach_drives_score_to_one():
    scorer = make_scorer()
    state = AccountState()
    state.apply_fill("ABC", +150, 100)  # 150 > 100 cap
    score, breaches, _ = scorer.score(state)
    assert score == 1.0
    assert any("position" in b for b in breaches)


def test_gross_notional_breach():
    scorer = make_scorer()
    state = AccountState()
    # Position within per-symbol cap (90 < 100) but huge price => notional breach.
    state.apply_fill("XYZ", +90, 20_000)  # notional 1_800_000 > 1_000_000
    score, breaches, gross = scorer.score(state)
    assert gross == 1_800_000
    assert score == 1.0
    assert any("notional" in b for b in breaches)


def test_buy_then_sell_nets_position():
    scorer = make_scorer()
    state = AccountState()
    state.apply_fill("ABC", +60, 100)
    state.apply_fill("ABC", -20, 100)  # net +40
    assert state.positions["ABC"] == 40
    score, breaches, gross = scorer.score(state)
    assert gross == 40 * 100
    assert breaches == []


def test_order_velocity_breach():
    scorer = make_scorer()
    state = AccountState()
    now = 1_000_000
    # 12 orders within the last minute, cap is 10 => breach.
    for i in range(12):
        state.record_order(now - i * 1000)  # all within 12s
    score, breaches, _ = scorer.score(state, now_millis=now)
    assert score == 1.0
    assert any("velocity" in b for b in breaches)


def test_gross_notional_does_not_net_across_symbols():
    # Long in one symbol, short in another: gross exposure must ADD (abs), not cancel.
    scorer = make_scorer()
    state = AccountState()
    state.apply_fill("AAA", +30, 1000)   # 30_000
    state.apply_fill("BBB", -30, 1000)   # 30_000 (abs)
    _, _, gross = scorer.score(state)
    assert gross == 60_000
