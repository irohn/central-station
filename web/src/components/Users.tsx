import { useEffect, useState } from "react";
import { api, ApiError } from "../api";
import type { Permission, Role, User } from "../types";
import { Check, Pencil, X } from "./Icons";

const roles: Role[] = ["listener", "collaborator", "manager", "admin"];

interface RolesInfo { permissions: Permission[]; grants: Record<Role, Permission[]> }

export function Users({ me, onMeChanged }: { me: User; onMeChanged: (u: User) => void }) {
  const [users, setUsers] = useState<User[]>([]);
  const [info, setInfo] = useState<RolesInfo | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [editing, setEditing] = useState<{ id: string; name: string } | null>(null);
  const [perms, setPerms] = useState<{ id: string; set: Set<Permission> } | null>(null);
  const [busy, setBusy] = useState(false);
  const isAdmin = me.permissions.includes("users:manage");
  const fail = (e: unknown) => setError(e instanceof ApiError ? e.message : String(e));

  const load = () => api<{ users: User[] }>("GET", "/users").then((r) => setUsers(r.users)).catch(fail);
  useEffect(() => {
    load();
    api<RolesInfo>("GET", "/roles").then(setInfo).catch(() => {});
  }, []);

  async function patch(id: string, body: Record<string, unknown>) {
    setError(null); setBusy(true);
    try {
      const u = await api<User>("PATCH", `/users/${id}`, body);
      if (u.id === me.id) onMeChanged({ ...me, ...u });
      await load();
      return true;
    } catch (e) { fail(e); return false; } finally { setBusy(false); }
  }
  async function saveName() {
    if (!editing) return;
    if (await patch(editing.id, { name: editing.name.trim() })) setEditing(null);
  }
  async function savePerms() {
    if (!perms) return;
    setError(null); setBusy(true);
    try {
      await api("PUT", `/users/${perms.id}/permissions`, { permissions: [...perms.set] });
      setPerms(null);
      await load();
    } catch (e) { fail(e); } finally { setBusy(false); }
  }
  async function resetPerms(id: string) {
    setError(null); setBusy(true);
    try {
      await api("DELETE", `/users/${id}/permissions`);
      setPerms(null);
      await load();
    } catch (e) { fail(e); } finally { setBusy(false); }
  }

  const allPerms = info?.permissions ?? [];

  return (
    <section className="card">
      <div className="row space-between">
        <h2>Users</h2>
        <span className="muted small">{users.length}</span>
      </div>
      <p className="muted small">
        {isAdmin
          ? "Roles are cumulative: listener < collaborator < manager < admin. Per-user permissions apply until the server restarts."
          : "You can change your own display name. Ask an admin for a different role."}
      </p>
      {error && <p className="error">{error}</p>}
      <ul className="user-list">
        {users.map((u) => {
          const mine = u.id === me.id;
          const canEdit = mine || isAdmin;
          const isEditing = editing?.id === u.id;
          const editingPerms = perms?.id === u.id;
          return (
            <li key={u.id} className="user-row-wrap">
              <div className="user-row">
                {u.pictureUrl ? <img src={u.pictureUrl} alt="" className="avatar lg" /> : <span className="avatar lg initial">{u.name.slice(0, 1)}</span>}
                <div className="track-meta">
                  {isEditing ? (
                    <form className="row" onSubmit={(e) => { e.preventDefault(); saveName(); }}>
                      <input value={editing.name} autoFocus maxLength={60} placeholder={u.displayName ? "" : u.name} aria-label="Display name" onChange={(e) => setEditing({ ...editing, name: e.target.value })} />
                      <button className="btn icon" type="submit" title="Save" disabled={busy}><Check width={16} height={16} /></button>
                      <button className="btn icon" type="button" title="Cancel" onClick={() => setEditing(null)}><X width={16} height={16} /></button>
                    </form>
                  ) : (
                    <div className="row wrap">
                      <span className="strong ellipsis">{u.name}</span>
                      {mine && <span className="muted small">(you)</span>}
                      {!isAdmin && <span className="pill role">{u.role}</span>}
                      {u.permissionsOverridden && <span className="pill warn-pill" title="Permissions were customised for this session">custom permissions</span>}
                    </div>
                  )}
                  <div className="muted small ellipsis">{u.email}{u.hasPassword ? " · password" : " · Google"}</div>
                  <div className="muted small">
                    {u.emailVerified ? "Verified" : <span className="warn">Not verified</span>} · last seen {new Date(u.lastLoginAt).toLocaleDateString()}
                  </div>
                </div>
                <div className="row actions">
                  {isAdmin && !u.emailVerified && <button className="btn" type="button" disabled={busy} onClick={() => patch(u.id, { emailVerified: true })}>Verify</button>}
                  {isAdmin && (
                    <select className="role-select" value={u.role} disabled={mine || busy} aria-label={`Role for ${u.name}`} onChange={(e) => patch(u.id, { role: e.target.value as Role })}>
                      {roles.map((r) => <option key={r} value={r}>{r}</option>)}
                    </select>
                  )}
                  {isAdmin && !mine && (
                    <button className="btn link" type="button" disabled={busy} onClick={() => setPerms(editingPerms ? null : { id: u.id, set: new Set(u.permissions) })}>
                      {editingPerms ? "Close" : "Permissions"}
                    </button>
                  )}
                  {canEdit && !isEditing && (
                    <button className="btn icon" type="button" title="Edit display name" aria-label={`Edit display name of ${u.name}`} disabled={busy} onClick={() => setEditing({ id: u.id, name: u.displayName ?? u.name })}>
                      <Pencil width={16} height={16} />
                    </button>
                  )}
                </div>
              </div>
              {editingPerms && perms && (
                <div className="perm-editor">
                  <p className="muted small">Tick the permissions {u.name} should have right now. This replaces the role's set for this API instance only; a restart puts them back on the role.</p>
                  <div className="perm-grid">
                    {allPerms.map((p) => (
                      <label key={p} className="perm-item">
                        <input type="checkbox" checked={perms.set.has(p)} onChange={(e) => { const set = new Set(perms.set); if (e.target.checked) set.add(p); else set.delete(p); setPerms({ id: u.id, set }); }} />
                        <span className="mono small">{p}</span>
                      </label>
                    ))}
                  </div>
                  <div className="row form-actions">
                    <button className="btn primary" type="button" disabled={busy} onClick={savePerms}>Apply</button>
                    <button className="btn" type="button" disabled={busy || !u.permissionsOverridden} onClick={() => resetPerms(u.id)}>Reset to role</button>
                    <button className="btn link" type="button" onClick={() => setPerms(null)}>Cancel</button>
                  </div>
                </div>
              )}
            </li>
          );
        })}
      </ul>
    </section>
  );
}
