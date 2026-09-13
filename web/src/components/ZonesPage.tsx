import { useState } from "react";
import { api, ApiError } from "../api";
import type { Zone, ZoneSummary } from "../types";
import { Check, Copy, Plus, RefreshCw, Trash } from "./Icons";

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

export function ZonesPage({ zones, zoneId, onSelect }: { zones: ZoneSummary[]; zoneId: string; onSelect: (id: string) => void }) {
  const [name, setName] = useState("");
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [issued, setIssued] = useState<Issued | null>(null);
  const [renaming, setRenaming] = useState<{ id: string; name: string } | null>(null);
  const fail = (e: unknown) => setError(e instanceof ApiError ? e.message : String(e));

  async function create(e: React.FormEvent) {
    e.preventDefault();
    setBusy("create"); setError(null);
    try {
      const r = await api<{ zone: Zone; token: string }>("POST", "/zones", { name: name.trim() });
      setIssued({ zone: r.zone.name, token: r.token });
      setName("");
    } catch (err) { fail(err); } finally { setBusy(null); }
  }
  async function rename() {
    if (!renaming) return;
    setBusy(renaming.id); setError(null);
    try {
      await api("PATCH", `/zones/${renaming.id}`, { name: renaming.name.trim() });
      setRenaming(null);
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
        <p className="muted small">Each zone is one player node with its own queue, default playlist and volume. Add a zone per room or site, then run a player container next to those speakers with the zone's token.</p>
        {error && <p className="error small">{error}</p>}
        <ul className="zone-list">
          {zones.map((z) => (
            <li key={z.id} className="zone-row">
              <span className={`dot ${z.player.online ? "on" : "off"}`} />
              <div className="zone-row-main">
                {renaming?.id === z.id ? (
                  <form className="row" onSubmit={(e) => { e.preventDefault(); rename(); }}>
                    <input value={renaming.name} autoFocus onChange={(e) => setRenaming({ ...renaming, name: e.target.value })} />
                    <button className="btn primary" type="submit" disabled={busy !== null || !renaming.name.trim()}>Save</button>
                    <button className="btn link" type="button" onClick={() => setRenaming(null)}>Cancel</button>
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
                {z.player.online && (
                  <div className="muted small ellipsis">
                    Output: {z.player.audioDevice === "auto"
                      ? `System default${z.player.activeAudioDevice && z.player.activeAudioDevice !== "auto" ? ` (${z.player.audioDevices.find((d) => d.id === z.player.activeAudioDevice)?.name ?? z.player.activeAudioDevice})` : ""}`
                      : z.player.audioDevices.find((d) => d.id === z.player.audioDevice)?.name ?? z.player.audioDevice}
                    {z.player.audioDeviceMissing ? " · not found, using default" : ""}
                  </div>
                )}
                <div className="muted small">
                  {z.hasToken ? "Player token issued" : z.isDefault ? "Uses PLAYER_TOKEN from the server environment (issue a token to switch)" : "No player token yet"}
                </div>
              </div>
              <div className="zone-actions">
                {z.id !== zoneId && <button className="btn link" type="button" onClick={() => onSelect(z.id)}>View</button>}
                <button className="btn icon" type="button" title="Rename" disabled={busy !== null} onClick={() => setRenaming({ id: z.id, name: z.name })}>✎</button>
                <button className="btn icon" type="button" title={z.hasToken ? "Issue a new player token" : "Issue a player token"} disabled={busy !== null} onClick={() => rotate(z)}><RefreshCw width={16} height={16} /></button>
                <button className="btn icon danger" type="button" title="Delete zone" disabled={busy !== null || zones.length <= 1} onClick={() => remove(z)}><Trash width={16} height={16} /></button>
              </div>
            </li>
          ))}
        </ul>
      </section>

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
    </>
  );
}
