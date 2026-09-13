import { useState } from "react";
import { api, ApiError } from "../api";
import type { User, Zone, ZoneSummary } from "../types";
import { Check, Copy, Pencil, Plus, RefreshCw, Trash } from "./Icons";

/** A freshly issued player token; shown once. */
interface Issued { zone: string; token: string }

function TokenPanel({ issued, onClose }: { issued: Issued; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  const origin = location.origin;
  const snippet = `docker run -d --name central-player \\
  -e API_URL=${origin} \\
  -e PLAYER_TOKEN=${issued.token} \\
  -e PLAYER_NAME="${issued.zone}" \\
  -e PULSE_SERVER=unix:/run/pulse/native \\
  -v /run/user/1000/pulse/native:/run/pulse/native \\
  --restart unless-stopped ghcr.io/irohn/central-station-player:latest`;
  async function copy() {
    try {
      await navigator.clipboard.writeText(issued.token);
      setCopied(true);
      window.setTimeout(() => setCopied(false), 2000);
    } catch {
      /* clipboard blocked; the token is still visible */
    }
  }
  return (
    <section className="card token-panel">
      <h2>Player token for {issued.zone}</h2>
      <p className="warn small">This token is shown once. Put it in the player container that sits next to this zone's speakers.</p>
      <div className="row token-row">
        <code className="mono token">{issued.token}</code>
        <button className="btn icon" type="button" title="Copy token" onClick={copy}>{copied ? <Check width={16} height={16} /> : <Copy width={16} height={16} />}</button>
      </div>
      <p className="muted small">Example on a Linux box with PulseAudio or PipeWire (adjust the API URL if this page is not reachable from there):</p>
      <pre className="mono snippet">{snippet}</pre>
      <button className="btn" type="button" onClick={onClose}>Done</button>
    </section>
  );
}

/** Edit state for one zone: its name (admins) and its output device (output:control). */
interface Editing { id: string; name: string; device: string }

export function ZonesPage({ zones, zoneId, user, onSelect }: { zones: ZoneSummary[]; zoneId: string; user: User; onSelect: (id: string) => void }) {
  const isAdmin = user.permissions.includes("integrations:manage");
  const canOutput = user.permissions.includes("output:control");
  const [name, setName] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [issued, setIssued] = useState<Issued | null>(null);
  const [editing, setEditing] = useState<Editing | null>(null);
  const fail = (e: unknown) => setError(e instanceof ApiError ? e.message : String(e));
  const deviceName = (z: ZoneSummary, id: string) => (id === "auto" ? "System default" : z.player.audioDevices.find((d) => d.id === id)?.name ?? id);

  async function create(e: React.FormEvent) {
    e.preventDefault();
    setBusy("create"); setError(null);
    try {
      const r = await api<{ zone: Zone; token: string }>("POST", "/zones", { name: name.trim() });
      setIssued({ zone: r.zone.name, token: r.token });
      setName("");
    } catch (err) { fail(err); } finally { setBusy(null); }
  }
  async function saveEdit() {
    if (!editing) return;
    const z = zones.find((o) => o.id === editing.id);
    if (!z) return;
    setBusy(editing.id); setError(null);
    try {
      if (isAdmin && editing.name.trim() !== z.name) await api("PATCH", `/zones/${editing.id}`, { name: editing.name.trim() });
      if (canOutput && editing.device !== (z.player.audioDevice || "auto")) await api("POST", `/zones/${editing.id}/playback/output`, { device: editing.device });
      setEditing(null);
    } catch (err) { fail(err); } finally { setBusy(null); }
  }
  async function rotate(z: ZoneSummary) {
    if (!window.confirm(`Issue a new player token for "${z.name}"? The current player will be disconnected until it gets the new token.`)) return;
    setBusy(z.id); setError(null);
    try {
      const r = await api<{ token: string }>("POST", `/zones/${z.id}/token`);
      setIssued({ zone: z.name, token: r.token });
    } catch (err) { fail(err); } finally { setBusy(null); }
  }
  async function remove(z: ZoneSummary) {
    if (!window.confirm(`Delete zone "${z.name}"? Its queue, history and settings are removed and its player is disconnected.`)) return;
    setBusy(z.id); setError(null);
    try {
      await api("DELETE", `/zones/${z.id}`);
      if (z.id === zoneId) {
        const next = zones.find((o) => o.id !== z.id);
        if (next) onSelect(next.id);
      }
    } catch (err) { fail(err); } finally { setBusy(null); }
  }

  return (
    <>
      {issued && <TokenPanel issued={issued} onClose={() => setIssued(null)} />}

      <section className="card">
        <h2>Zones</h2>
        <p className="muted small">{isAdmin ? "Each zone is one player node with its own queue, default playlist and volume. Add a zone per room or site, then run a player container next to those speakers with the zone's token." : "Each zone is one player node. Use the pencil to choose which speakers a zone plays through."}</p>
        {error && <p className="error small">{error}</p>}
        <ul className="zone-list">
          {zones.map((z) => (
            <li key={z.id} className="zone-row">
              <span className={`dot ${z.player.online ? "on" : "off"}`} />
              <div className="zone-row-main">
                {editing?.id === z.id ? (
                  <form className="zone-edit" onSubmit={(e) => { e.preventDefault(); saveEdit(); }}>
                    {isAdmin && (
                      <label className="field-inline">
                        <span className="muted small">Name</span>
                        <input value={editing.name} autoFocus maxLength={60} onChange={(e) => setEditing({ ...editing, name: e.target.value })} />
                      </label>
                    )}
                    {canOutput && (
                      <label className="field-inline">
                        <span className="muted small">Output</span>
                        <select value={editing.device} onChange={(e) => setEditing({ ...editing, device: e.target.value })}>
                          <option value="auto">System default{z.player.activeAudioDevice && z.player.audioDevice === "auto" && z.player.activeAudioDevice !== "auto" ? ` (${deviceName(z, z.player.activeAudioDevice)})` : ""}</option>
                          {z.player.audioDevices.map((d) => <option key={d.id} value={d.id}>{d.name}</option>)}
                          {editing.device !== "auto" && !z.player.audioDevices.some((d) => d.id === editing.device) && <option value={editing.device}>{editing.device} (not found)</option>}
                        </select>
                        {!z.player.online && <span className="muted small">player offline: choices apply when it connects</span>}
                      </label>
                    )}
                    <div className="row">
                      <button className="btn primary" type="submit" disabled={busy !== null || (isAdmin && !editing.name.trim())}>Save</button>
                      <button className="btn link" type="button" onClick={() => setEditing(null)}>Cancel</button>
                    </div>
                  </form>
                ) : (
                  <div className="row wrap">
                    <span className="strong">{z.name}</span>
                    {z.isDefault && <span className="pill">default</span>}
                    {z.id === zoneId && <span className="pill role">viewing</span>}
                  </div>
                )}
                <div className="muted small">
                  {z.player.online ? `Player online${z.player.name ? ` · ${z.player.name}` : ""}` : "No player connected"}
                  {" · "}{z.current ? `${z.status}: ${z.current.track.title}` : "idle"}{z.queued > 0 ? ` · ${z.queued} queued` : ""}
                </div>
                {z.player.fault && (
                  <div className="warn small ellipsis" title={z.player.fault.message}>
                    Paused: {z.player.fault.kind === "output" ? "no audio output" : "playback keeps failing"}
                  </div>
                )}
                {editing?.id !== z.id && (z.player.online || z.player.audioDevice !== "auto") && (
                  <div className="muted small ellipsis">
                    Output: {z.player.audioDevice === "auto"
                      ? `System default${z.player.activeAudioDevice && z.player.activeAudioDevice !== "auto" ? ` (${deviceName(z, z.player.activeAudioDevice)})` : ""}`
                      : deviceName(z, z.player.audioDevice)}
                    {z.player.audioDeviceMissing ? " · not found, using default" : ""}
                  </div>
                )}
                {isAdmin && (
                  <div className="muted small">
                    {z.hasToken ? "Player token issued" : z.isDefault ? "Uses PLAYER_TOKEN from the server environment (issue a token to switch)" : "No player token yet"}
                  </div>
                )}
              </div>
              <div className="zone-actions">
                {z.id !== zoneId && <button className="btn link" type="button" onClick={() => onSelect(z.id)}>View</button>}
                {(isAdmin || canOutput) && editing?.id !== z.id && (
                  <button className="btn icon" type="button" title={isAdmin ? "Edit name and output" : "Choose output"} disabled={busy !== null} onClick={() => setEditing({ id: z.id, name: z.name, device: z.player.audioDevice || "auto" })}><Pencil width={16} height={16} /></button>
                )}
                {isAdmin && <button className="btn icon" type="button" title={z.hasToken ? "Issue a new player token" : "Issue a player token"} disabled={busy !== null} onClick={() => rotate(z)}><RefreshCw width={16} height={16} /></button>}
                {isAdmin && <button className="btn icon danger" type="button" title="Delete zone" disabled={busy !== null || zones.length <= 1} onClick={() => remove(z)}><Trash width={16} height={16} /></button>}
              </div>
            </li>
          ))}
        </ul>
      </section>

      {isAdmin && (
      <form className="card" onSubmit={create}>
        <h2>Add a zone</h2>
        <label className="field">
          <span className="field-label">Name</span>
          <input value={name} placeholder="Kitchen, 2nd floor, Berlin office…" maxLength={60} onChange={(e) => setName(e.target.value)} disabled={busy !== null} />
        </label>
        <div className="row form-actions">
          <button className="btn primary with-icon" type="submit" disabled={busy !== null || !name.trim()}><Plus width={16} height={16} /> {busy === "create" ? "Creating…" : "Create zone"}</button>
        </div>
      </form>
      )}
    </>
  );
}
