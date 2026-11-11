import { useEffect, useState } from "react";
import { api, ApiError, loginUrl } from "../api";
import type { AuthMethods, Role, User, WhoAmI } from "../types";

type Mode = "signin" | "register";

const errorText: Record<string, string> = {
  verification_link_invalid: "That verification link is invalid or has expired. Sign in and request a new one.",
  email_not_allowed: "This email address is not allowed to sign in.",
  email_not_verified: "Google did not report this email as verified.",
};

const noMethods: AuthMethods = { mode: "google", proxy: false, google: false, dev: false, password: false, passwordVerification: "admin", passwordMinLength: 10 };

/** Why the proxy left this request anonymous, for a person to act on. */
const proxyReason: Record<string, string> = {
  no_identity: "This connection carries no identity. Open Central Station through the tailnet, from a machine that is signed in as you (not a tagged one).",
  untrusted_peer: "The request did not come through the tailnet proxy, so its identity is not believed.",
  invalid_identity: "The identity the proxy sent is not an email address.",
  email_not_allowed: "Your account is not allowed to use Central Station. Ask an admin to add it.",
};

export function Login({ onLogin }: { onLogin: (u: User) => void }) {
  const [methods, setMethods] = useState<AuthMethods | null>(null);
  const [mode, setMode] = useState<Mode>("signin");
  const [email, setEmail] = useState("");
  const [name, setName] = useState("");
  const [password, setPassword] = useState("");
  const [confirm, setConfirm] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [devEmail, setDevEmail] = useState("");
  const [role, setRole] = useState<Role>("collaborator");
  const [who, setWho] = useState<WhoAmI | null>(null);
  const params = new URLSearchParams(location.search);
  const urlError = params.get("error");
  const verified = params.get("verified") === "1";

  useEffect(() => {
    api<AuthMethods>("GET", "/auth/methods").then(setMethods).catch(() => setMethods(noMethods));
  }, []);

  // Proxy mode has no button to press: the proxy either named us or it did
  // not, and whoami says which. Signed in after all (the page was opened
  // before the proxy was ready, say) means we can go straight in.
  const checkProxy = () => api<WhoAmI>("GET", "/auth/whoami").then((w) => { if (w.signedIn && w.user) onLogin(w.user); else setWho(w); }).catch(fail);
  useEffect(() => { if (methods?.proxy) void checkProxy(); }, [methods?.proxy]); // eslint-disable-line react-hooks/exhaustive-deps

  const fail = (e: unknown) => setError(e instanceof ApiError ? e.message : String(e));
  const minLen = methods?.passwordMinLength ?? 10;

  async function submitPassword(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    if (mode === "register") {
      if (password !== confirm) { setError("The passwords do not match."); return; }
      if (password.length < minLen) { setError(`Use at least ${minLen} characters.`); return; }
    }
    setBusy(true);
    try {
      const path = mode === "register" ? "/auth/password/register" : "/auth/password/login";
      const body = mode === "register" ? { email, name, password } : { email, password };
      const res = await api<{ user: User }>("POST", path, body);
      onLogin(res.user);
    } catch (err) { fail(err); } finally { setBusy(false); }
  }

  async function devLogin(e: React.FormEvent) {
    e.preventDefault();
    setError(null);
    try {
      const res = await api<{ user: User }>("POST", "/auth/dev/login", { email: devEmail, role });
      onLogin(res.user);
    } catch (err) { fail(err); }
  }

  return (
    <div className="login">
      <div className="card login-card">
        <h1>Central Station</h1>
        <p className="muted">The office queue. Sign in to see what is playing.</p>
        {verified && <p className="ok small">Your email is verified. Sign in to continue.</p>}
        {urlError && <p className="error">{errorText[urlError] ?? `Sign-in failed: ${urlError.replace(/_/g, " ")}`}</p>}

        {methods?.proxy && (
          <div className="proxy-login">
            <p className="muted small">You are signed in by your tailnet identity; there is no password here.</p>
            {who && !who.signedIn && <p className="error">{proxyReason[who.reason ?? ""] ?? who.message ?? "Not signed in."}</p>}
            {who?.message && who.reason && proxyReason[who.reason] && <p className="muted small">{who.message}</p>}
            <button className="btn primary" type="button" onClick={() => { setError(null); void checkProxy(); }}>Try again</button>
          </div>
        )}

        {methods?.google && (
          <a className="btn primary" href={loginUrl("/")}>Sign in with Google</a>
        )}

        {methods?.password && (
          <form onSubmit={submitPassword} className="password-login">
            {methods.google && <div className="or"><span>or</span></div>}
            <div className="tabs seg" role="tablist">
              <button type="button" role="tab" className={mode === "signin" ? "active" : ""} onClick={() => { setMode("signin"); setError(null); }}>Sign in</button>
              <button type="button" role="tab" className={mode === "register" ? "active" : ""} onClick={() => { setMode("register"); setError(null); }}>Register</button>
            </div>
            {mode === "register" && (
              <input type="text" placeholder="Your name" value={name} onChange={(e) => setName(e.target.value)} autoComplete="name" />
            )}
            <input type="email" required placeholder="you@example.com" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" inputMode="email" />
            <input type="password" required placeholder={mode === "register" ? `Password (${minLen}+ characters)` : "Password"} value={password} onChange={(e) => setPassword(e.target.value)} autoComplete={mode === "register" ? "new-password" : "current-password"} minLength={mode === "register" ? minLen : undefined} />
            {mode === "register" && (
              <input type="password" required placeholder="Confirm password" value={confirm} onChange={(e) => setConfirm(e.target.value)} autoComplete="new-password" />
            )}
            <button className="btn primary" type="submit" disabled={busy}>{busy ? "Please wait…" : mode === "register" ? "Create account" : "Sign in"}</button>
            {mode === "register" && (
              <p className="muted small">
                {methods.passwordVerification === "email"
                  ? "We will email you a link to confirm your address. Until then you can listen but not add songs."
                  : "An admin confirms new accounts. Until then you can listen but not add songs."}
              </p>
            )}
          </form>
        )}

        {methods && !methods.proxy && !methods.google && !methods.dev && !methods.password && (
          <p className="error">No sign-in method is configured on the server.</p>
        )}
        {error && <p className="error">{error}</p>}

        {methods?.dev && (
          <form onSubmit={devLogin} className="dev-login">
            <h3>Developer login</h3>
            <p className="muted small">AUTH_DEV_MODE is on. Pick any email and role.</p>
            <input type="email" required placeholder="you@example.com" value={devEmail} onChange={(e) => setDevEmail(e.target.value)} />
            <select value={role} onChange={(e) => setRole(e.target.value as Role)}>
              <option value="listener">listener</option>
              <option value="collaborator">collaborator</option>
              <option value="manager">manager</option>
              <option value="admin">admin</option>
            </select>
            <button className="btn" type="submit">Sign in</button>
          </form>
        )}
      </div>
    </div>
  );
}
