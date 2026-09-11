import { useEffect, useState } from "react";
import { api, ApiError } from "../api";
import type { Role, User } from "../types";

const roles: Role[] = ["listener", "collaborator", "manager", "admin"];

export function Users({ me }: { me: User }) {
  const [users, setUsers] = useState<User[]>([]);
  const [error, setError] = useState<string | null>(null);

  const load = () => api<{ users: User[] }>("GET", "/users").then((r) => setUsers(r.users)).catch((e) => setError(String(e)));
  useEffect(() => {
    load();
  }, []);

  async function setRole(id: string, role: Role) {
    setError(null);
    try {
      await api("PATCH", `/users/${id}`, { role });
      await load();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    }
  }
  async function verify(id: string) {
    setError(null);
    try {
      await api("PATCH", `/users/${id}`, { emailVerified: true });
      await load();
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    }
  }

  return (
    <section className="card">
      <div className="row space-between">
        <h2>Users</h2>
        <span className="muted small">{users.length}</span>
      </div>
      <p className="muted small">Roles are cumulative: listener &lt; collaborator &lt; manager &lt; admin.</p>
      {error && <p className="error">{error}</p>}
      <ul className="user-list">
        {users.map((u) => (
          <li key={u.id} className="user-row">
            {u.pictureUrl ? <img src={u.pictureUrl} alt="" className="avatar lg" /> : <span className="avatar lg initial">{u.name.slice(0, 1)}</span>}
            <div className="track-meta">
              <div className="strong ellipsis">{u.name}{u.id === me.id ? <span className="muted"> (you)</span> : ""}</div>
              <div className="muted small ellipsis">{u.email}{u.hasPassword ? " · password" : " · Google"}</div>
              <div className="muted small">
                {u.emailVerified ? "Verified" : <span className="warn">Not verified</span>} · last seen {new Date(u.lastLoginAt).toLocaleDateString()}
              </div>
            </div>
            {!u.emailVerified && <button className="btn" type="button" onClick={() => verify(u.id)}>Verify</button>}
            <select className="role-select" value={u.role} disabled={u.id === me.id} aria-label={`Role for ${u.name}`} onChange={(e) => setRole(u.id, e.target.value as Role)}>
              {roles.map((r) => <option key={r} value={r}>{r}</option>)}
            </select>
          </li>
        ))}
      </ul>
    </section>
  );
}
