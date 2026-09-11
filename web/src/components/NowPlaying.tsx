import { useState } from "react";
import { api, ApiError, fmtTime } from "../api";
import type { PlayerStatus, QueueState, User } from "../types";

export function NowPlaying({ queue, player, user }: { queue: QueueState; player: PlayerStatus; user: User }) {
  const [busy, setBusy] = useState<string | null>(null);
  const [error, setError] = useState<string | null>(null);
  const canControl = user.permissions.includes("playback:control");
  const cur = queue.current;
  const pos = player.itemId === cur?.id ? player.positionSeconds : 0;
  const dur = player.itemId === cur?.id && player.durationSeconds > 0 ? player.durationSeconds : cur?.track.durationSeconds ?? 0;
  const pct = dur > 0 ? Math.min(100, (pos / dur) * 100) : 0;

  async function act(action: "skip" | "pause" | "resume") {
    setBusy(action);
    setError(null);
    try {
      await api("POST", `/playback/${action}`);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    } finally {
      setBusy(null);
    }
  }

  return (
    <section className="card now-playing">
      <div className="row space-between">
        <h2>Now playing</h2>
        <span className={`pill ${queue.status}`}>{queue.status}</span>
      </div>
      {!cur ? (
        <p className="muted">Nothing is playing. Add a song to get things going.</p>
      ) : (
        <div className="track-row big">
          {cur.track.thumbnailUrl && <img className="thumb" src={cur.track.thumbnailUrl} alt="" />}
          <div className="track-meta">
            <a className="title" href={cur.track.url} target="_blank" rel="noreferrer">{cur.track.title}</a>
            <div className="muted">{cur.track.artist ?? cur.track.provider} · added by {cur.addedByName}</div>
            <div className="progress"><div className="bar" style={{ width: `${pct}%` }} /></div>
            <div className="muted small">{fmtTime(pos)} / {dur > 0 ? fmtTime(dur) : "–:––"}</div>
          </div>
        </div>
      )}
      {canControl && cur && (
        <div className="row controls">
          {queue.status === "paused" ? (
            <button className="btn" disabled={!!busy} onClick={() => act("resume")}>Resume</button>
          ) : (
            <button className="btn" disabled={!!busy} onClick={() => act("pause")}>Pause</button>
          )}
          <button className="btn" disabled={!!busy} onClick={() => act("skip")}>Skip</button>
        </div>
      )}
      {error && <p className="error">{error}</p>}
      {!player.online && cur && <p className="warn small">No player is connected, so nothing is actually coming out of the speakers.</p>}
      {player.lastError && <p className="warn small">Player error on the last track: {player.lastError}</p>}
    </section>
  );
}
