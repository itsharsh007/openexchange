package com.openexchange.engine.ledger;

import com.openexchange.engine.model.Trade;
import java.util.List;

/**
 * Pure double-entry posting rules — no database, no Spring — so the balancing invariant can be unit
 * tested directly.
 *
 * <p>When the buyer acquires {@code quantity} shares at {@code priceTicks}, four postings result:
 *
 * <pre>
 *   buyer : CASH   -price*qty      buyer : SYMBOL +qty
 *   seller: CASH   +price*qty      seller: SYMBOL -qty
 * </pre>
 *
 * <p>The signed deltas of each asset sum to zero — no money or shares are created or destroyed.
 */
public final class DoubleEntry {

    /** Asset code for cash balances (amount stored in ticks, matching order prices). */
    public static final String CASH = "CASH";

    private DoubleEntry() {}

    /** The balanced set of postings for one trade. Cash uses the resting (execution) price. */
    public static List<LedgerEntry> entriesFor(Trade t) {
        long cash = Math.multiplyExact(t.priceTicks(), t.quantity());
        return List.of(
                new LedgerEntry(t.buyAccountId(), CASH, -cash),
                new LedgerEntry(t.buyAccountId(), t.symbol(), t.quantity()),
                new LedgerEntry(t.sellAccountId(), CASH, cash),
                new LedgerEntry(t.sellAccountId(), t.symbol(), -t.quantity()));
    }
}
