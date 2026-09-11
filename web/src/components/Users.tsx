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

  return (
    <section className="card">
      <h2>Users</h2>
      {error && <p className="error">{error}</p>}
      <table className="users">
        <thead>
          <tr><th>Name</th><th>Email</th><th>Role</th><th>Last login</th></tr>
        </thead>
        <tbody>
          {users.map((u) => (
            <tr key={u.id}>
              <td>{u.name}{u.id === me.id ? " (you)" : ""}</td>
              <td className="muted">{u.email}</td>
              <td>
                <select value={u.role} disabled={u.id === me.id} onChange={(e) => setRole(u.id, e.target.value as Role)}>
                  {roles.map((r) => <option key={r} value={r}>{r}</option>)}
                </select>
              </td>
              <td className="muted small">{new Date(u.lastLoginAt).toLocaleString()}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </section>
  );
}
