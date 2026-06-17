import { formatTime } from "../util/format";
import type { RiskSignal } from "../types";
import styles from "./RiskPanel.module.css";

// Risk / exposure signals from the Python risk service (via Kafka → gateway → WS).
//
// Displays the per-account risk state: current exposure score, action
// (ALLOW/REJECT), breach reason, and signal timestamp. The panel turns red when
// the account is REJECTED so a blocked state is impossible to miss.

interface RiskPanelProps {
  signal: RiskSignal | null;
}

function pct(x: number) {
  return `${Math.round(x * 100)}%`;
}

export function RiskPanel({ signal }: RiskPanelProps) {
  if (!signal) {
    return (
      <section className={styles.panel}>
        <header className={styles.header}>
          <h2>Risk / ML</h2>
        </header>
        <p className={styles.muted}>awaiting risk signals…</p>
      </section>
    );
  }

  const isBlocked = signal.action === "REJECT";

  return (
    <section className={`${styles.panel} ${isBlocked ? styles.alert : ""}`}>
      <header className={styles.header}>
        <h2>Risk / ML</h2>
        {isBlocked && <span className={styles.badge}>BLOCKED</span>}
      </header>

      <div className={styles.block}>
        <div className={styles.label}>Account</div>
        <div className={styles.value}>{signal.accountId || "—"}</div>
      </div>

      <div className={styles.block}>
        <div className={styles.label}>
          Exposure score
          <span className={styles.muted}> ({signal.kind})</span>
        </div>
        <Bar value={signal.score} warn={signal.score >= 0.8} />
        <div className={styles.scoreRow}>
          <span className={isBlocked ? styles.down : styles.up}>
            {isBlocked ? "▼ REJECT" : "▲ ALLOW"}
          </span>
          <span className={styles.muted}>{pct(signal.score)}</span>
        </div>
      </div>

      {isBlocked && (
        <div className={styles.block}>
          <div className={styles.label}>Reason</div>
          <div className={`${styles.value} ${styles.reason}`}>{signal.reason}</div>
        </div>
      )}

      <div className={styles.block}>
        <div className={styles.muted}>Updated {formatTime(signal.tsMillis)}</div>
      </div>
    </section>
  );
}

function Bar({ value, warn }: { value: number; warn: boolean }) {
  const clamped = Math.max(0, Math.min(1, value));
  return (
    <div className={styles.barTrack}>
      <div
        className={`${styles.barFill} ${warn ? styles.warnFill : styles.infoFill}`}
        style={{ width: `${clamped * 100}%` }}
      />
    </div>
  );
}
