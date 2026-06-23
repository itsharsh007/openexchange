import { useState } from "react";
import { ensureGuest, login, signup } from "../api/session";
import styles from "./LoginScreen.module.css";

// Auth gate for the full-stack build. Offers login / signup against the gateway's
// password auth, plus a "continue as guest" path (the same anonymous demo session
// the public link uses). onAuthed fires once a token has been obtained.

type Mode = "login" | "signup";

export function LoginScreen({ onAuthed }: { onAuthed: () => void }) {
  const [mode, setMode] = useState<Mode>("login");
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  async function run(fn: () => Promise<void>) {
    setBusy(true);
    setError("");
    try {
      await fn();
      onAuthed();
    } catch (e) {
      setError(e instanceof Error ? e.message : "something went wrong");
      setBusy(false);
    }
  }

  function submit(e: React.FormEvent) {
    e.preventDefault();
    run(() => (mode === "login" ? login(email, password) : signup(email, password)));
  }

  return (
    <div className={styles.screen}>
      <div className={styles.card}>
        <h1 className={styles.brand}>
          OpenExchange <span className={styles.tag}>simulation</span>
        </h1>
        <p className={styles.sub}>
          {mode === "login" ? "Sign in to your account" : "Create an account"}
        </p>

        <form onSubmit={submit} className={styles.form}>
          <label>
            Email
            <input
              type="email"
              autoComplete="email"
              value={email}
              onChange={(e) => setEmail(e.target.value)}
              required
            />
          </label>
          <label>
            Password
            <input
              type="password"
              autoComplete={mode === "login" ? "current-password" : "new-password"}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              minLength={8}
              required
            />
          </label>

          {error && <div className={styles.error}>{error}</div>}

          <button type="submit" className={styles.primary} disabled={busy}>
            {busy ? "…" : mode === "login" ? "Sign in" : "Sign up"}
          </button>
        </form>

        <div className={styles.switch}>
          {mode === "login" ? (
            <>
              New here?{" "}
              <button type="button" onClick={() => { setMode("signup"); setError(""); }}>
                Create an account
              </button>
            </>
          ) : (
            <>
              Already have one?{" "}
              <button type="button" onClick={() => { setMode("login"); setError(""); }}>
                Sign in
              </button>
            </>
          )}
        </div>

        <div className={styles.divider}><span>or</span></div>

        <button
          type="button"
          className={styles.guest}
          disabled={busy}
          onClick={() => run(async () => { await ensureGuest(); })}
        >
          Continue as guest
        </button>
        <p className={styles.note}>Guest sessions are anonymous and use play money.</p>
      </div>
    </div>
  );
}
