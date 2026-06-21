import { useCallback, useEffect, useRef, useState } from "react";
import { DEMO_TOKEN, WS_URL } from "../config";
import type {
  ClientMessage,
  ConnectionState,
  ServerMessage,
  WsChannel,
} from "../types";

// ─────────────────────────────────────────────────────────────────────────────
// useWebSocket — a robust live-feed hook for the trading dashboard.
//
// Responsibilities (the things that make a real-time UI not fall over):
//  1. Connect to the gateway's /ws endpoint and parse the JSON envelope into a
//     typed ServerMessage discriminated union.
//  2. Automatic reconnect with EXPONENTIAL BACKOFF + JITTER so a gateway restart
//     doesn't turn into a reconnect storm (thundering herd) from every browser.
//  3. Subscription management: remember which channels/symbol we want, and
//     (re)send the subscribe frame on every (re)connect so state survives drops.
//  4. Surface a ConnectionState so the UI can show link health.
//
// WHY a ref-heavy design: WebSocket and timers are imperative, long-lived
// objects that must NOT be recreated on every render. We keep them in refs and
// only push *data* into React state, so re-renders never tear down the socket.
// ─────────────────────────────────────────────────────────────────────────────

interface UseWebSocketOptions {
  /** Channels to subscribe to. */
  channels: WsChannel[];
  /** Symbol to subscribe for (book/trades/risk are per-symbol). */
  symbol: string;
  /** Called for every successfully parsed server message. */
  onMessage: (msg: ServerMessage) => void;
  /** Backoff tuning (sane defaults below). */
  baseDelayMs?: number;
  maxDelayMs?: number;
}

interface UseWebSocketResult {
  connectionState: ConnectionState;
  /** Send an arbitrary client control frame (rarely needed directly). */
  send: (msg: ClientMessage) => void;
}

export function useWebSocket(opts: UseWebSocketOptions): UseWebSocketResult {
  const {
    channels,
    symbol,
    onMessage,
    baseDelayMs = 500, // first retry after ~0.5s
    maxDelayMs = 30_000, // cap backoff at 30s
  } = opts;

  const [connectionState, setConnectionState] =
    useState<ConnectionState>("connecting");

  // Imperative handles that must persist across renders.
  const wsRef = useRef<WebSocket | null>(null);
  const retryRef = useRef(0); // consecutive failed attempts → backoff exponent
  const reconnectTimerRef = useRef<ReturnType<typeof setTimeout> | null>(null);
  const closedByUserRef = useRef(false); // true on unmount → stop reconnecting

  // Keep the latest callback/subscription in refs so the connect logic can read
  // current values without being a dependency (which would reconnect on change).
  const onMessageRef = useRef(onMessage);
  const channelsRef = useRef(channels);
  const symbolRef = useRef(symbol);
  onMessageRef.current = onMessage;
  channelsRef.current = channels;
  symbolRef.current = symbol;

  // Send a frame if the socket is open; otherwise silently drop (the subscribe
  // frame will be re-sent on the next successful connect, so no state is lost).
  const send = useCallback((msg: ClientMessage) => {
    const ws = wsRef.current;
    if (ws && ws.readyState === WebSocket.OPEN) {
      ws.send(JSON.stringify(msg));
    }
  }, []);

  // Compute the next backoff delay: base * 2^attempt, capped, plus jitter.
  // Jitter (random 0–1x of the step) spreads reconnects so many clients don't
  // all retry on the same tick after a gateway blip.
  const nextDelay = useCallback(() => {
    const exp = Math.min(retryRef.current, 6); // cap exponent so 2^n doesn't blow up
    const capped = Math.min(baseDelayMs * 2 ** exp, maxDelayMs);
    const jitter = Math.random() * capped * 0.3;
    return capped + jitter;
  }, [baseDelayMs, maxDelayMs]);

  useEffect(() => {
    closedByUserRef.current = false;

    // connect() is recursive-ish: on close it schedules itself via setTimeout.
    const connect = () => {
      setConnectionState(retryRef.current === 0 ? "connecting" : "reconnecting");

      const url = DEMO_TOKEN ? `${WS_URL}?token=${encodeURIComponent(DEMO_TOKEN)}` : WS_URL;
      const ws = new WebSocket(url);
      wsRef.current = ws;

      ws.onopen = () => {
        retryRef.current = 0; // reset backoff on a healthy connection
        setConnectionState("open");
        // (Re)send our subscription so server-side state is restored after a drop.
        ws.send(
          JSON.stringify({
            type: "subscribe",
            channels: channelsRef.current,
            symbol: symbolRef.current,
          } satisfies ClientMessage),
        );
      };

      ws.onmessage = (event) => {
        try {
          const parsed = JSON.parse(event.data as string) as ServerMessage;
          onMessageRef.current(parsed);
        } catch (err) {
          // A malformed frame must never crash the feed; log and keep going.
          console.error("[ws] failed to parse message", err);
        }
      };

      ws.onerror = () => {
        // onerror is always followed by onclose; let onclose drive reconnect.
      };

      ws.onclose = () => {
        wsRef.current = null;
        if (closedByUserRef.current) {
          setConnectionState("closed");
          return;
        }
        // Schedule a backed-off reconnect.
        retryRef.current += 1;
        setConnectionState("reconnecting");
        const delay = nextDelay();
        reconnectTimerRef.current = setTimeout(connect, delay);
      };
    };

    connect();

    // Cleanup on unmount: stop reconnecting and close the socket cleanly.
    return () => {
      closedByUserRef.current = true;
      if (reconnectTimerRef.current) clearTimeout(reconnectTimerRef.current);
      wsRef.current?.close();
    };
    // We deliberately depend only on nextDelay (stable). Channel/symbol changes
    // are handled by the separate effect below WITHOUT tearing down the socket.
  }, [nextDelay]);

  // When the desired subscription changes, send a new subscribe frame on the
  // existing open socket — no reconnect needed.
  useEffect(() => {
    send({ type: "subscribe", channels, symbol });
  }, [channels, symbol, send]);

  return { connectionState, send };
}
