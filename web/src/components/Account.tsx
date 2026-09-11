import { useState } from "react";
import { api, ApiError } from "../api";
import type { User } from "../types";

/** Account page: change password (password accounts only). */
export function Account({ user }: { user: User }) {
  const [current, setCurrent] = useState("");
  const [next, setNext] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [ok, setOk] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    setError(null); setOk(null);
    if (next !== confirm) { setError("The new passwords do not match."); return; }
    setBusy(true);
    try {
      await api("POST", "/auth/password/change", { currentPassword: current, newPassword: next });
      setCurrent(""); setNext(""); setConfirm("");
      setOk("Password changed.");
    } catch (err) { setError(err instanceof ApiError ? err.message : String(err)); } finally { setBusy(false); }
  }

  return (
    <section className="card">
      <h2>Account</h2>
      <p className="muted small">{user.email} · {user.role}{user.emailVerified ? " · verified" : " · not verified"}</p>
      {user.hasPassword ? (
        <form onSubmit={submit} className="account-form">
          <h3>Change password</h3>
          <input type="password" required placeholder="Current password" value={current} onChange={(e) => setCurrent(e.target.value)} autoComplete="current-password" />
          <input type="password" required placeholder="New password (10+ characters)" value={next} onChange={(e) => setNext(e.target.value)} autoComplete="new-password" minLength={10} />
          <input type="password" required placeholder="Confirm new password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" />
          <button className="btn primary" type="submit" disabled={busy}>{busy ? "Saving…" : "Change password"}</button>
          {error && <p className="error small">{error}</p>}
          {ok && <p className="ok small">{ok}</p>}
        </form>
      ) : (
        <p className="muted small">You sign in with Google, so there is no password to change here.</p>
      )}
    </section>
  );
}
