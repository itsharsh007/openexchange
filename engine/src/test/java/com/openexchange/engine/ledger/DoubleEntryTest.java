package com.openexchange.engine.ledger;

import static org.junit.jupiter.api.Assertions.assertEquals;

import com.openexchange.engine.model.Trade;
import java.util.HashMap;
import java.util.Map;
import org.junit.jupiter.api.Test;

/** The core money invariant: a trade's postings net to zero for every asset. */
class DoubleEntryTest {

    private Trade trade(long price, long qty) {
        return new Trade("AAPL-T1", "AAPL", price, qty, "bo", "so", "buyer", "seller", 1L);
    }

    @Test
    void postingsNetToZeroPerAsset() {
        var entries = DoubleEntry.entriesFor(trade(15000, 10));

        Map<String, Long> netByAsset = new HashMap<>();
        for (LedgerEntry e : entries) {
            netByAsset.merge(e.asset(), e.delta(), Long::sum);
        }

        // Nothing created or destroyed: cash and shares each sum to zero across the trade.
        assertEquals(0L, netByAsset.get(DoubleEntry.CASH), "cash must net to zero");
        assertEquals(0L, netByAsset.get("AAPL"), "shares must net to zero");
    }

    @Test
    void buyerPaysCashAndReceivesShares() {
        var entries = DoubleEntry.entriesFor(trade(15000, 10));

        long buyerCash =
                entries.stream()
                        .filter(e -> e.accountId().equals("buyer") && e.asset().equals(DoubleEntry.CASH))
                        .mapToLong(LedgerEntry::delta)
                        .sum();
        long buyerShares =
                entries.stream()
                        .filter(e -> e.accountId().equals("buyer") && e.asset().equals("AAPL"))
                        .mapToLong(LedgerEntry::delta)
                        .sum();

        assertEquals(-150000L, buyerCash, "buyer pays price*qty in cash");
        assertEquals(10L, buyerShares, "buyer receives the shares");
    }
}
