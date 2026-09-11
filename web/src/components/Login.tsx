import { useEffect, useState } from "react";
import { api, ApiError, loginUrl } from "../api";
import type { AuthMethods, Role, User } from "../types";

export function Login({ onLogin }: { onLogin: (u: User) => void }) {
  const [methods, setMethods] = useState<AuthMethods | null>(null);
  const [email, setEmail] = useState("");
  const [role, setRole] = useState<Role>("collaborator");
  const [error, setError] = useState<string | null>(null);
  const urlError = new URLSearchParams(location.search).get("error");

  useEffect(() => {
    api<AuthMethods>("GET", "/auth/methods").then(setMethods).catch(() => setMethods({ google: false, dev: false }));
  }, []);

  async function devLogin(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      const res = await api<{ user: User }>("POST", "/auth/dev/login", { email, role });
      onLogin(res.user);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    }
  }

  return (
    <div className="login">
      <div className="card login-card">
        <h1>Central Station</h1>
        <p className="muted">The office queue. Sign in to see what is playing.</p>
        {urlError && <p className="error">Sign-in failed: {urlError.replace(/_/g, " ")}</p>}
        {methods?.google && (
          <a className="btn primary" href={loginUrl("/")}>
            Sign in with Google
          </a>
        )}
        {methods && !methods.google && !methods.dev && (
          <p className="error">No sign-in method is configured on the server.</p>
        )}
        {methods?.dev && (
          <form onSubmit={devLogin} className="dev-login">
            <h3>Developer login</h3>
            <p className="muted small">AUTH_DEV_MODE is on. Pick any email and role.</p>
            <input type="email" required placeholder="you@example.com" value={email} onChange={(e) => setEmail(e.target.value)} />
            <select value={role} onChange={(e) => setRole(e.target.value as Role)}>
              <option value="listener">listener</option>
              <option value="collaborator">collaborator</option>
              <option value="manager">manager</option>
              <option value="admin">admin</option>
            </select>
            <button className="btn" type="submit">Sign in</button>
            {error && <p className="error">{error}</p>}
          </form>
        )}
      </div>
    </div>
  );
}
