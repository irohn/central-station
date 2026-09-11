import { useEffect, useRef, useState } from "react";
import { api, ApiError } from "../api";
import type { Clip, ClipsResponse, PlayerStatus, User } from "../types";
import { Megaphone, Mic, Play, Square, Trash } from "./Icons";

type Source = "record" | "type" | "saved";

function fmtDur(s?: number) {
  if (!s) return "";
  return s < 10 ? `${s.toFixed(1)}s` : `${Math.round(s)}s`;
}

/** Plays a clip through the browser for preview. */
function PreviewButton({ clipId, src }: { clipId?: string; src?: string }) {
  const ref = useRef<HTMLAudioElement>(null);
  const [playing, setPlaying] = useState(false);
  const url = src ?? (clipId ? `/api/v1/clips/${clipId}/audio` : undefined);
  if (!url) return null;
  return (
    <>
      <audio ref={ref} src={url} preload="none" onEnded={() => setPlaying(false)} onPause={() => setPlaying(false)} onPlay={() => setPlaying(true)} />
      <button
        className="btn icon" type="button" title={playing ? "Stop" : "Listen"} aria-label={playing ? "Stop" : "Listen"}
        onClick={() => { const a = ref.current; if (!a) return; if (playing) { a.pause(); a.currentTime = 0; } else void a.play(); }}
      >
        {playing ? <Square width={16} height={16} /> : <Play width={16} height={16} />}
      </button>
    </>
  );
}

export function Broadcast({ user, player }: { user: User; player: PlayerStatus }) {
  const isAdmin = user.permissions.includes("settings:manage");
  const [info, setInfo] = useState<ClipsResponse | null>(null);
  const [source, setSource] = useState<Source>("record");
  const [selected, setSelected] = useState<Clip | null>(null);
  const [loops, setLoops] = useState(1);
  const [chime, setChime] = useState<string>(""); // "" = default, "none", or id
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [ok, setOk] = useState<string | null>(null);

  // Recording state
  const [recording, setRecording] = useState(false);
  const [elapsed, setElapsed] = useState(0);
  const [blob, setBlob] = useState<Blob | null>(null);
  const [blobUrl, setBlobUrl] = useState<string | null>(null);
  const [recName, setRecName] = useState("");
  const recorder = useRef<MediaRecorder | null>(null);
  const chunks = useRef<BlobPart[]>([]);
  const startedAt = useRef(0);
  const timer = useRef<number | undefined>(undefined);

  // TTS state
  const [text, setText] = useState("");
  const [ttsName, setTtsName] = useState("");

  const load = () => api<ClipsResponse>("GET", "/clips").then(setInfo).catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
  useEffect(() => { load(); }, []);
  useEffect(() => () => { if (blobUrl) URL.revokeObjectURL(blobUrl); }, [blobUrl]);

  const clips = info?.clips ?? [];
  const chimes = clips.filter((c) => c.kind === "chime");
  const saved = clips.filter((c) => c.kind !== "chime");
  const defaultChime = chimes.find((c) => c.id === info?.defaultChimeId);
  const maxSec = info?.maxDurationSec ?? 60;
  const canRecord = typeof MediaRecorder !== "undefined" && !!navigator.mediaDevices?.getUserMedia;

  function fail(e: unknown) { setError(e instanceof ApiError ? e.message : e instanceof Error ? e.message : String(e)); }

  async function startRecording() {
    setError(null); setOk(null);
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      const mime = ["audio/mp4", "audio/webm;codecs=opus", "audio/webm", "audio/ogg;codecs=opus"].find((m) => MediaRecorder.isTypeSupported(m)) ?? "";
      const rec = new MediaRecorder(stream, mime ? { mimeType: mime } : undefined);
      chunks.current = [];
      rec.ondataavailable = (e) => { if (e.data.size > 0) chunks.current.push(e.data); };
      rec.onstop = () => {
        stream.getTracks().forEach((t) => t.stop());
        const b = new Blob(chunks.current, { type: rec.mimeType || mime || "audio/webm" });
        setBlob(b);
        setBlobUrl((old) => { if (old) URL.revokeObjectURL(old); return URL.createObjectURL(b); });
        setRecording(false);
        window.clearInterval(timer.current);
      };
      recorder.current = rec;
      startedAt.current = Date.now();
      setElapsed(0);
      setBlob(null);
      rec.start(250);
      setRecording(true);
      timer.current = window.setInterval(() => {
        const s = (Date.now() - startedAt.current) / 1000;
        setElapsed(s);
        if (s >= maxSec) stopRecording();
      }, 200);
    } catch (e) {
      fail(e instanceof Error && e.name === "NotAllowedError" ? new Error("Microphone access was denied. Allow it in the browser settings and try again.") : e);
    }
  }
  function stopRecording() {
    const rec = recorder.current;
    if (rec && rec.state !== "inactive") rec.stop();
  }
  async function saveRecording() {
    if (!blob) return;
    setBusy("save"); setError(null);
    try {
      const ext = blob.type.includes("mp4") ? "m4a" : blob.type.includes("ogg") ? "ogg" : "webm";
      const fd = new FormData();
      fd.append("file", blob, `recording.${ext}`);
      fd.append("name", recName.trim());
      fd.append("kind", "recording");
      fd.append("mime", blob.type);
      fd.append("durationSeconds", String(Math.min(elapsed, maxSec).toFixed(1)));
      const res = await fetch("/api/v1/clips", { method: "POST", credentials: "include", body: fd });
      const data = await res.json();
      if (!res.ok) throw new ApiError(res.status, data?.error?.code ?? "http_error", data?.error?.message ?? `HTTP ${res.status}`);
      const c = data as Clip;
      await load();
      setSelected(c);
      setBlob(null); setRecName("");
      setOk(`Saved "${c.name}". Choose repeat and chime, then broadcast.`);
    } catch (e) { fail(e); } finally { setBusy(null); }
  }
  async function createTTS() {
    setBusy("tts"); setError(null); setOk(null);
    try {
      const c = await api<Clip>("POST", "/clips/tts", { name: ttsName.trim(), text: text.trim() });
      await load();
      setSelected(c);
      setText(""); setTtsName("");
      setOk(`Created "${c.name}". Listen to it, then broadcast.`);
    } catch (e) { fail(e); } finally { setBusy(null); }
  }
  async function send() {
    if (!selected) return;
    setBusy("send"); setError(null); setOk(null);
    try {
      const r = await api<{ position: number }>("POST", "/broadcasts", { clipId: selected.id, loops, chimeId: chime });
      setOk(r.position === 0 ? `Broadcasting "${selected.name}" now.` : `Queued "${selected.name}" (${r.position} ahead of it).`);
    } catch (e) { fail(e); } finally { setBusy(null); }
  }
  async function rename(c: Clip) {
    const name = window.prompt("Rename clip", c.name);
    if (!name || name.trim() === c.name) return;
    try { await api("PATCH", `/clips/${c.id}`, { name: name.trim() }); await load(); } catch (e) { fail(e); }
  }
  async function remove(c: Clip) {
    if (!window.confirm(`Delete "${c.name}"?`)) return;
    try { await api("DELETE", `/clips/${c.id}`); if (selected?.id === c.id) setSelected(null); await load(); } catch (e) { fail(e); }
  }
  async function setDefaultChime(id: string) {
    try { await api("PUT", "/settings/chime", { clipId: id }); await load(); } catch (e) { fail(e); }
  }
  async function uploadChime(file: File) {
    setBusy("chime"); setError(null);
    try {
      const fd = new FormData();
      fd.append("file", file, file.name);
      fd.append("name", file.name.replace(/\.[^.]+$/, ""));
      fd.append("kind", "chime");
      fd.append("mime", file.type);
      const res = await fetch("/api/v1/clips", { method: "POST", credentials: "include", body: fd });
      const data = await res.json();
      if (!res.ok) throw new ApiError(res.status, data?.error?.code ?? "http_error", data?.error?.message ?? `HTTP ${res.status}`);
      await load();
    } catch (e) { fail(e); } finally { setBusy(null); }
  }

  if (!info && !error) return <div className="center muted">Loading…</div>;

  return (
    <>
      <section className="card">
        <div className="row space-between wrap">
          <h2>Send an announcement</h2>
          <span className={`muted small`}>{player.online ? "Player online" : "No player connected"}</span>
        </div>
        {player.broadcast && (
          <p className="ok small">Playing now: "{player.broadcast.clipName}" by {player.broadcast.byName}{player.broadcastsQueued > 0 ? ` · ${player.broadcastsQueued} waiting` : ""}</p>
        )}

        <div className="tabs seg" role="tablist">
          <button role="tab" className={source === "record" ? "active" : ""} onClick={() => setSource("record")}>Record</button>
          {info?.ttsAvailable && <button role="tab" className={source === "type" ? "active" : ""} onClick={() => setSource("type")}>Type</button>}
          <button role="tab" className={source === "saved" ? "active" : ""} onClick={() => setSource("saved")}>Saved ({saved.length})</button>
        </div>

        {source === "record" && (
          <div className="source-pane">
            {!canRecord ? (
              <p className="muted small">This browser cannot record audio. Use the Saved tab{info?.ttsAvailable ? " or Type" : ""}.</p>
            ) : (
              <>
                <div className="rec-row">
                  {!recording ? (
                    <button className="btn primary with-icon rec-btn" type="button" onClick={startRecording} disabled={busy !== null}><Mic /> {blob ? "Record again" : "Start recording"}</button>
                  ) : (
                    <button className="btn danger with-icon rec-btn recording" type="button" onClick={stopRecording}><Square /> Stop · {elapsed.toFixed(0)}s</button>
                  )}
                  <span className="muted small">up to {maxSec}s</span>
                </div>
                {blob && blobUrl && (
                  <div className="rec-review">
                    <audio controls src={blobUrl} className="rec-audio" />
                    <div className="row wrap">
                      <input placeholder="Name this clip (optional)" value={recName} onChange={(e) => setRecName(e.target.value)} />
                      <button className="btn" type="button" disabled={busy !== null} onClick={saveRecording}>{busy === "save" ? "Saving…" : "Save clip"}</button>
                    </div>
                  </div>
                )}
              </>
            )}
          </div>
        )}

        {source === "type" && (
          <div className="source-pane">
            <textarea
              placeholder="Type the announcement… (English voice)"
              rows={3}
              maxLength={info?.maxTextChars}
              value={text}
              onChange={(e) => setText(e.target.value)}
            />
            <div className="row wrap">
              <input placeholder="Name (optional)" value={ttsName} onChange={(e) => setTtsName(e.target.value)} />
              <button className="btn" type="button" disabled={!text.trim() || busy !== null} onClick={createTTS}>{busy === "tts" ? "Generating…" : "Generate"}</button>
            </div>
          </div>
        )}

        {source === "saved" && (
          <div className="source-pane">
            {saved.length === 0 ? <p className="muted small">No saved clips yet.</p> : (
              <ul className="clip-list">
                {saved.map((c) => (
                  <li key={c.id} className={`clip-row${selected?.id === c.id ? " selected" : ""}`}>
                    <button className="clip-select" type="button" onClick={() => setSelected(c)} aria-pressed={selected?.id === c.id}>
                      <span className="strong ellipsis">{c.name}</span>
                      <span className="muted small">{c.kind === "tts" ? "typed" : "recording"} · {fmtDur(c.durationSeconds)} · {c.createdByName}</span>
                    </button>
                    <PreviewButton clipId={c.id} />
                    <button className="btn icon" type="button" title="Rename" onClick={() => rename(c)}>✎</button>
                    {(c.createdById === user.id || isAdmin) && <button className="btn icon danger" type="button" title="Delete" onClick={() => remove(c)}><Trash width={16} height={16} /></button>}
                  </li>
                ))}
              </ul>
            )}
          </div>
        )}

        <div className="send-box">
          <div className="send-selected">
            <Megaphone className="muted" />
            {selected ? (
              <div className="track-meta">
                <div className="strong ellipsis">{selected.name}</div>
                <div className="muted small">{selected.kind === "tts" ? "typed announcement" : "recording"} {fmtDur(selected.durationSeconds)}</div>
              </div>
            ) : (
              <div className="muted small">Record, type, or pick a saved clip to broadcast.</div>
            )}
            {selected && <PreviewButton clipId={selected.id} />}
          </div>
          <div className="row wrap send-options">
            <label className="field-inline">
              <span className="muted small">Repeat</span>
              <select value={loops} onChange={(e) => setLoops(Number(e.target.value))}>
                {Array.from({ length: info?.maxLoops ?? 10 }, (_, i) => i + 1).map((n) => <option key={n} value={n}>{n}×</option>)}
              </select>
            </label>
            <label className="field-inline">
              <span className="muted small">Chime</span>
              <select value={chime} onChange={(e) => setChime(e.target.value)}>
                <option value="">Default{defaultChime ? ` (${defaultChime.name})` : ""}</option>
                <option value="none">None</option>
                {chimes.map((c) => <option key={c.id} value={c.id}>{c.name}</option>)}
              </select>
            </label>
            <button className="btn primary with-icon send-btn" type="button" disabled={!selected || !player.online || busy !== null} onClick={send}>
              <Megaphone /> {busy === "send" ? "Sending…" : "Broadcast now"}
            </button>
          </div>
          {!player.online && <p className="warn small">The player is offline, so announcements cannot be delivered.</p>}
        </div>
        {error && <p className="error">{error}</p>}
        {ok && <p className="ok small">{ok}</p>}
      </section>

      {isAdmin && (
        <section className="card">
          <h2>Chimes</h2>
          <p className="muted small">The default chime plays before every announcement that does not pick another one.</p>
          <ul className="clip-list">
            {chimes.map((c) => (
              <li key={c.id} className="clip-row">
                <label className="clip-select radio">
                  <input type="radio" name="default-chime" checked={info?.defaultChimeId === c.id} onChange={() => setDefaultChime(c.id)} />
                  <span className="strong">{c.name}</span>
                  <span className="muted small">{c.builtin ? "built-in" : c.createdByName} · {fmtDur(c.durationSeconds)}</span>
                </label>
                <PreviewButton clipId={c.id} />
                {!c.builtin && <button className="btn icon danger" type="button" title="Delete" onClick={() => remove(c)}><Trash width={16} height={16} /></button>}
              </li>
            ))}
          </ul>
          <label className="btn upload-btn">
            {busy === "chime" ? "Uploading…" : "Upload a chime"}
            <input type="file" accept="audio/*" hidden disabled={busy !== null} onChange={(e) => { const f = e.target.files?.[0]; if (f) uploadChime(f); e.target.value = ""; }} />
          </label>
        </section>
      )}
    </>
  );
}
