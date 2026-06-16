import type { RiskSignal } from "../types";
import { ticksToPrice } from "../util/format";
import styles from "./RiskPanel.module.css";

// ML / risk signals from the Python risk service (via Kafka → gateway → WS).
//
// Shows the three model outputs described in docs/architecture.md:
//   1. price prediction (next-tick direction + confidence + predicted price)
//   2. anomaly / fraud score (with an alert flag)
//   3. per-account risk/exposure score
// Bars give an at-a-glance read; the anomaly flag flips the panel into an alert
// state so risky flow is impossible to miss.

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

  const dirClass =
    signal.predictedDirection === "UP"
      ? styles.up
      : signal.predictedDirection === "DOWN"
        ? styles.down
        : styles.flat;

  return (
    <section
      className={`${styles.panel} ${signal.anomalyFlag ? styles.alert : ""}`}
    >
      <header className={styles.header}>
        <h2>Risk / ML</h2>
        {signal.anomalyFlag && <span className={styles.badge}>ANOMALY</span>}
      </header>

      {/* 1. Price prediction */}
      <div className={styles.block}>
        <div className={styles.label}>Next-tick prediction</div>
        <div className={styles.predRow}>
          <span className={`${styles.dir} ${dirClass}`}>
            {signal.predictedDirection === "UP"
              ? "▲"
              : signal.predictedDirection === "DOWN"
                ? "▼"
                : "►"}{" "}
            {signal.predictedDirection}
          </span>
          <span className={styles.predPrice}>
            → {ticksToPrice(signal.predictedPriceTicks)}
          </span>
          <span className={styles.muted}>conf {pct(signal.confidence)}</span>
        </div>
        <Bar value={signal.confidence} kind="info" />
      </div>

      {/* 2. Anomaly / fraud */}
      <div className={styles.block}>
        <div className={styles.label}>Anomaly score</div>
        <Bar value={signal.anomalyScore} kind="warn" />
        <span className={styles.muted}>{pct(signal.anomalyScore)}</span>
      </div>

      {/* 3. Risk / exposure */}
      <div className={styles.block}>
        <div className={styles.label}>Account risk</div>
        <Bar value={signal.riskScore} kind="warn" />
        <span className={styles.muted}>{pct(signal.riskScore)}</span>
      </div>
    </section>
  );
}

function Bar({ value, kind }: { value: number; kind: "info" | "warn" }) {
  const clamped = Math.max(0, Math.min(1, value));
  return (
    <div className={styles.barTrack}>
      <div
        className={`${styles.barFill} ${kind === "warn" ? styles.warnFill : styles.infoFill}`}
        style={{ width: `${clamped * 100}%` }}
      />
    </div>
  );
}
