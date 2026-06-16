package com.openexchange.engine.ledger;

/**
 * One signed posting in the double-entry ledger: {@code account_id} gains {@code delta} of
 * {@code asset}. Asset is either {@link DoubleEntry#CASH} (amount in ticks) or a symbol (shares).
 * Deltas are signed — debits negative, credits positive.
 */
public record LedgerEntry(String accountId, String asset, long delta) {}
