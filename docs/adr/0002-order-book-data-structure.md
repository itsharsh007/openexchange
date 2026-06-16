# ADR 0002 — Order book data structure

- **Status:** Accepted
- **Date:** 2026-06-16

## Context
The matching engine must find the best bid/ask quickly, insert/cancel orders at arbitrary price
levels, and preserve price-time priority.

## Decision
Model each side of the book as a **sorted map of price level → FIFO queue of orders**:
- Bids: a map ordered by price **descending**; asks **ascending** (best price at the head).
- Each price level holds a queue preserving arrival order (time priority).
- A side-table maps `orderId → (priceLevel, node)` for O(1) cancel/amend lookups.

In Java this is a `TreeMap<Long, ArrayDeque<Order>>` keyed by price in integer ticks (avoid floating
point for money), with a `HashMap<orderId, ...>` index.

## Complexity
| Operation | Cost |
|---|---|
| Best bid/ask | O(1) (peek head of map) |
| Insert order | O(log P) for the price level + O(1) enqueue (P = distinct price levels) |
| Cancel by id | O(1) lookup + O(1) removal |
| Match step | O(1) per fill against the head |

## Alternatives considered
- **Two heaps (priority queues):** O(log n) best price, but cancel-by-id is O(n) without extra
  bookkeeping; FIFO within a price is awkward. Rejected.
- **Flat array of price levels:** O(1) at a level but wasteful/unbounded across wide price ranges.

## Consequences
- Prices stored as integer ticks (no float rounding bugs in money math).
- Clear Big-O story for reviews.
