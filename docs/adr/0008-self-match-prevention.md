# ADR 0008 — Self-match prevention (skip policy)

- **Status:** Accepted
- **Date:** 2026-06-24

## Context
The matching engine paired any two crossing orders on price-time priority alone. It never checked
whether the resting order and the incoming order belonged to the **same account**. As a result a
single trader could trade with themselves: placing a BUY and a crossing SELL on the same symbol
produced real fills, printed trades on the tape, and inflated volume — while the account's net
position and cash stayed flat. This is **wash trading**, and on a real venue it's prohibited and
prevented in the engine. It also produced confusing demo behaviour: a lone visitor on the public
link (one account) could "trade" and see fills despite no counterparty.

Both engines were affected: the Java matching engine (`engine/`) and the in-gateway Go `LocalEngine`
(`ENGINE_MODE=local`) that powers the public demo.

## Decision
Add **self-match prevention (STP)** with a **skip** policy: an order never matches against another
order from the **same account**. The taker's own resting orders are passed over — they stay resting,
untouched — and the taker matches only against *other* accounts' liquidity. Any unmatched remainder
rests as normal.

We chose **skip** over the two common alternatives:

- *Cancel-oldest* (cancel the resting order) — silently removes standing liquidity the trader still
  wants.
- *Cancel-newest* (reject the incoming order) — bounces the aggressor entirely.

Skip is the least surprising: nothing is cancelled or rejected, you simply cannot trade with
yourself. The trade-off is a possible same-account *locked book* (your own bid and ask resting at the
same price). That's harmless — STP guarantees those two orders never match — and it keeps both order
intents alive.

### Implementation
- **Java** (`OrderBook.submit`): walk price levels with an explicit iterator (so a level made up
  entirely of the taker's own orders is passed over rather than re-selected as `firstEntry()`
  forever). Within a level, same-account resting orders are held aside, the rest are matched, then
  the held orders are restored to the **front** in original order so their time priority is
  preserved.
- **Go** (`LocalEngine.bestOpposite`): the best-opposite scan skips resting orders whose
  `accountID` equals the taker's, so they're never selected as the maker. (The scan already runs per
  fill, so no restore step is needed.)
- **Market orders** that can only reach their own liquidity now find nothing fillable and are
  rejected, exactly as if the book were empty.

## Consequences
- A single account can no longer generate fake fills/volume; solo use of the public link shows
  orders **resting** instead of self-trading.
- Cross-account matching — the behaviour the live demo depends on — is unchanged: different visitors
  still trade with each other.
- Tests that previously crossed two orders on a shared dummy account now assign **distinct accounts
  per side** (the realistic setup). New tests cover: self-match is prevented and both orders rest;
  the taker skips its own order but fills another account's order behind it at the same level; and a
  market order can't sweep its own liquidity.
- Not implemented: configurable STP modes (cancel-oldest/newest), or cross-symbol/parent-account
  grouping. Skip is sufficient and is the most intuitive default for this system.
