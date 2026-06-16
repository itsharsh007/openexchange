package com.openexchange.engine.ledger;

import com.openexchange.engine.model.Trade;
import java.util.List;
import org.springframework.jdbc.core.JdbcTemplate;
import org.springframework.stereotype.Repository;
import org.springframework.transaction.annotation.Transactional;

/**
 * Postgres-backed {@link TradeLedger}. Writes the trade row and its balanced postings in a single
 * transaction, so a trade is either fully recorded or not at all — the ledger never ends up
 * half-written.
 */
@Repository
public class JdbcTradeLedger implements TradeLedger {

    private final JdbcTemplate jdbc;

    public JdbcTradeLedger(JdbcTemplate jdbc) {
        this.jdbc = jdbc;
    }

    @Override
    @Transactional
    public void record(Trade t) {
        // Idempotent on trade_id: a retry (same trade) inserts nothing and posts no duplicate
        // entries. trade_id is engine-assigned and unique per execution.
        int inserted =
                jdbc.update(
                        "INSERT INTO trades(trade_id, symbol, price_ticks, quantity, buy_order_id,"
                                + " sell_order_id, ts_millis) VALUES (?,?,?,?,?,?,?)"
                                + " ON CONFLICT (trade_id) DO NOTHING",
                        t.tradeId(),
                        t.symbol(),
                        t.priceTicks(),
                        t.quantity(),
                        t.buyOrderId(),
                        t.sellOrderId(),
                        t.tsMillis());
        if (inserted == 0) {
            return; // already recorded
        }
        List<LedgerEntry> entries = DoubleEntry.entriesFor(t);
        jdbc.batchUpdate(
                "INSERT INTO ledger_entries(trade_id, account_id, asset, delta) VALUES (?,?,?,?)",
                entries.stream()
                        .map(e -> new Object[] {t.tradeId(), e.accountId(), e.asset(), e.delta()})
                        .toList());
    }
}
