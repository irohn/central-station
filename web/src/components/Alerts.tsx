import { useEffect, useState } from "react";
import { api, ApiError } from "../api";
import type { Alert, AlertsResponse, Clip, ClipsResponse, PlayerStatus } from "../types";
import { Switch } from "./Switch";
import { Clock, Megaphone, Trash } from "./Icons";

const DAYS = ["Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"]; // cron: 1..6, 0 (Sunday)
const DAY_CRON = [1, 2, 3, 4, 5, 6, 0];

/** Simple form state: a time plus a set of weekdays, or a raw cron line. */
interface Simple { time: string; days: boolean[] }

function toCron(s: Simple): string {
  const [h, m] = s.time.split(":").map(Number);
  const all = s.days.every(Boolean);
  const days = all ? "*" : DAY_CRON.filter((_, i) => s.days[i]).join(",");
  return `${m} ${h} * * ${days || "*"}`;
}

/** Parses "M H * * D" back into the simple form; null for anything richer. */
function fromCron(cron: string): Simple | null {
  const m = /^(\d{1,2}) (\d{1,2}) \* \* ([\d,]+|\*)$/.exec(cron.trim());
  if (!m) return null;
  const days = DAY_CRON.map(() => m[3] === "*");
  if (m[3] !== "*") m[3].split(",").forEach((d) => { const i = DAY_CRON.indexOf(Number(d)); if (i >= 0) days[i] = true; });
  return { time: `${m[2].padStart(2, "0")}:${m[1].padStart(2, "0")}`, days };
}

function describe(cron: string): string {
  const s = fromCron(cron);
  if (!s) return cron;
  const on = DAYS.filter((_, i) => s.days[i]);
  const when = on.length === 7 ? "Every day" : on.length === 5 && !s.days[5] && !s.days[6] ? "Weekdays" : on.length === 0 ? "Never" : on.join(", ");
  return `${when} at ${s.time}`;
}

function relative(iso?: string): string {
  if (!iso) return "";
  const diff = (new Date(iso).getTime() - Date.now()) / 1000;
  const abs = Math.abs(diff);
  const unit = abs < 3600 ? [Math.round(abs / 60), "min"] : abs < 86400 ? [Math.round(abs / 3600), "h"] : [Math.round(abs / 86400), "d"];
  return diff >= 0 ? `in ${unit[0]} ${unit[1]}` : `${unit[0]} ${unit[1]} ago`;
}

const empty = (tz: string) => ({ name: "", enabled: true, schedule: "0 12 * * 1-5", timezone: tz, clipId: "", loops: 1, chimeId: "" });

export function Alerts({ player }: { player: PlayerStatus }) {
  const [info, setInfo] = useState<AlertsResponse | null>(null);
  const [clips, setClips] = useState<ClipsResponse | null>(null);
  const [editing, setEditing] = useState<Partial<Alert> | null>(null);
  const [simple, setSimple] = useState<Simple | null>(null); // null = raw cron mode
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [ok, setOk] = useState<string | null>(null);

  const load = () => Promise.all([api<AlertsResponse>("GET", "/alerts"), api<ClipsResponse>("GET", "/clips")])
    .then(([a, c]) => { setInfo(a); setClips(c); })
    .catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
  useEffect(() => { load(); }, []);

  const fail = (e: unknown) => setError(e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e));
  const saved: Clip[] = (clips?.clips ?? []).filter((c) => c.kind !== "chime");
  const chimes: Clip[] = (clips?.clips ?? []).filter((c) => c.kind === "chime");
  const clipName = (id: string) => clips?.clips.find((c) => c.id === id)?.name ?? "(missing clip)";

  function startEdit(a?: Alert) {
    const base = a ? { ...a } : empty(info?.defaultTimezone ?? "UTC");
    setEditing(base);
    setSimple(fromCron(base.schedule ?? "") ?? (a ? null : { time: "12:00", days: [true, true, true, true, true, false, false] }));
    setError(null); setOk(null);
  }
  function setSchedule(s: Simple) { setSimple(s); setEditing((e) => ({ ...e, schedule: toCron(s) })); }

  async function save() {
    if (!editing) return;
    setBusy("save"); setError(null);
    try {
      const body = { name: editing.name, enabled: editing.enabled, schedule: editing.schedule, timezone: editing.timezone, clipId: editing.clipId, loops: editing.loops, chimeId: editing.chimeId ?? "" };
      if (editing.id) await api("PATCH", `/alerts/${editing.id}`, body);
      else await api("POST", "/alerts", body);
      setEditing(null);
      await load();
      setOk("Saved.");
    } catch (e) { fail(e); } finally { setBusy(null); }
  }
  async function toggle(a: Alert) {
    try { await api("PATCH", `/alerts/${a.id}`, { enabled: !a.enabled }); await load(); } catch (e) { fail(e); }
  }
  async function remove(a: Alert) {
    if (!window.confirm(`Delete alert "${a.name}"?`)) return;
    try { await api("DELETE", `/alerts/${a.id}`); await load(); } catch (e) { fail(e); }
  }
  async function test(a: Alert) {
    setBusy(a.id); setError(null); setOk(null);
    try { await api("POST", `/alerts/${a.id}/test`); setOk(`Playing "${a.name}" now.`); } catch (e) { fail(e); } finally { setBusy(null); }
  }

  if (!info && !error) return <div className="center muted">Loading…</div>;
  const list = info?.alerts ?? [];

  return (
    <>
      {!editing && (
        <section className="card">
          <div className="row space-between wrap">
            <h2>Scheduled announcements</h2>
            <button className="btn primary" type="button" onClick={() => startEdit()} disabled={saved.length === 0}>New alert</button>
          </div>
          {saved.length === 0 && <p className="muted small">Record or type a clip on the Broadcast page first; alerts play saved clips.</p>}
          {error && <p className="error small">{error}</p>}
          {ok && <p className="ok small">{ok}</p>}
          {list.length === 0 ? <p className="muted">No alerts yet.</p> : (
            <ul className="alert-list">
              {list.map((a) => (
                <li key={a.id} className={`alert-row${a.enabled ? "" : " off"}`}>
                  <div className="alert-main">
                    <div className="row wrap">
                      <span className="strong">{a.name}</span>
                      {a.lastError && <span className="pill error" title={a.lastError}>failed</span>}
                    </div>
                    <div className="muted small"><Clock width={14} height={14} /> {describe(a.schedule)}{a.timezone !== info?.defaultTimezone ? ` (${a.timezone})` : ""}</div>
                    <div className="muted small">Plays "{clipName(a.clipId)}"{a.loops > 1 ? ` ${a.loops}×` : ""}{a.chimeId === "none" ? ", no chime" : ""}</div>
                    <div className="muted small">
                      {a.enabled && a.nextFireAt ? `Next ${relative(a.nextFireAt)} · ${new Date(a.nextFireAt).toLocaleString([], { weekday: "short", hour: "2-digit", minute: "2-digit" })}` : "Paused"}
                      {a.lastFiredAt ? ` · last ${relative(a.lastFiredAt)}` : ""}
                    </div>
                  </div>
                  <div className="alert-actions">
                    <Switch label="" checked={a.enabled} onChange={() => toggle(a)} />
                    <button className="btn icon" type="button" title="Play now" disabled={busy !== null || !player.online} onClick={() => test(a)}><Megaphone width={16} height={16} /></button>
                    <button className="btn icon" type="button" title="Edit" onClick={() => startEdit(a)}>✎</button>
                    <button className="btn icon danger" type="button" title="Delete" onClick={() => remove(a)}><Trash width={16} height={16} /></button>
                  </div>
                </li>
              ))}
            </ul>
          )}
        </section>
      )}

      {editing && (
        <section className="card">
          <h2>{editing.id ? "Edit alert" : "New alert"}</h2>
          <label className="field">
            <span className="field-label">Name</span>
            <input value={editing.name ?? ""} onChange={(e) => setEditing({ ...editing, name: e.target.value })} placeholder="Lunch is here" />
          </label>

          <div className="field">
            <span className="field-label">When</span>
            {simple ? (
              <>
                <div className="row wrap">
                  <input type="time" value={simple.time} onChange={(e) => setSchedule({ ...simple, time: e.target.value })} className="time-input" />
                  <div className="day-picker">
                    {DAYS.map((d, i) => (
                      <button key={d} type="button" className={`day${simple.days[i] ? " on" : ""}`} aria-pressed={simple.days[i]}
                        onClick={() => { const days = [...simple.days]; days[i] = !days[i]; setSchedule({ ...simple, days }); }}>{d}</button>
                    ))}
                  </div>
                </div>
                <button className="btn link" type="button" onClick={() => setSimple(null)}>Advanced (cron)</button>
              </>
            ) : (
              <>
                <input value={editing.schedule ?? ""} onChange={(e) => setEditing({ ...editing, schedule: e.target.value })} placeholder="0 12 * * 1-5" className="mono" />
                <p className="muted small">minute hour day-of-month month day-of-week · {describe(editing.schedule ?? "")}</p>
                {fromCron(editing.schedule ?? "") && <button className="btn link" type="button" onClick={() => setSimple(fromCron(editing.schedule ?? ""))}>Simple</button>}
              </>
            )}
          </div>

          <label className="field">
            <span className="field-label">Timezone</span>
            <input value={editing.timezone ?? ""} onChange={(e) => setEditing({ ...editing, timezone: e.target.value })} placeholder="Asia/Jerusalem" />
          </label>

          <label className="field">
            <span className="field-label">Clip</span>
            <select value={editing.clipId ?? ""} onChange={(e) => setEditing({ ...editing, clipId: e.target.value })}>
              <option value="">Choose a clip…</option>
              {saved.map((c) => <option key={c.id} value={c.id}>{c.name} ({c.kind === "tts" ? "typed" : "recording"})</option>)}
            </select>
          </label>

          <div className="row wrap send-options">
            <label className="field-inline">
              <span className="muted small">Repeat</span>
              <select value={editing.loops ?? 1} onChange={(e) => setEditing({ ...editing, loops: Number(e.target.value) })}>
                {Array.from({ length: info?.maxLoops ?? 10 }, (_, i) => i + 1).map((n) => <option key={n} value={n}>{n}×</option>)}
              </select>
            </label>
            <label className="field-inline">
              <span className="muted small">Chime</span>
              <select value={editing.chimeId ?? ""} onChange={(e) => setEditing({ ...editing, chimeId: e.target.value })}>
                <option value="">Default</option>
                <option value="none">None</option>
                {chimes.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
              </select>
            </label>
          </div>
          <Switch label="Enabled" checked={editing.enabled ?? true} onChange={(v) => setEditing({ ...editing, enabled: v })} />

          <div className="row form-actions">
            <button className="btn primary" type="button" disabled={busy !== null || !editing.name?.trim() || !editing.clipId} onClick={save}>{busy === "save" ? "Saving…" : "Save alert"}</button>
            <button className="btn link" type="button" disabled={busy !== null} onClick={() => setEditing(null)}>Cancel</button>
          </div>
          {error && <p className="error small">{error}</p>}
        </section>
      )}
    </>
  );
}
