import { useState } from "react";
import { api, ApiError } from "../api";
import type { QueueState, User } from "../types";

export function QueueList({ queue, user }: { queue: QueueState; user: User }) {
  const [error, setError] = useState<string | null>(null);
  const canRemoveAny = user.permissions.includes("queue:remove_any");
  const canRemoveOwn = user.permissions.includes("queue:remove_own");
  const canReorder = user.permissions.includes("queue:reorder");

  async function remove(id: string) {
    setError(null);
    try {
      await api("DELETE", `/queue/${id}`);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    }
  }
  async function move(id: string, position: number) {
    setError(null);
    try {
      await api("PATCH", `/queue/${id}`, { position });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    }
  }

  return (
    <section className="card">
      <div className="row space-between">
        <h2>Up next</h2>
        <span className="muted">{queue.items.length} queued</span>
      </div>
      {error && <p className="error">{error}</p>}
      {queue.items.length === 0 ? (
        <p className="muted">
          {queue.fallback?.enabled && queue.fallback.next
            ? "Nobody has queued anything. Add a song and it plays right after the current track."
            : "The queue is empty."}
        </p>
      ) : (
        <ol className="queue">
          {queue.items.map((it, i) => {
            const mine = it.addedById === user.id;
            const removable = canRemoveAny || (canRemoveOwn && mine);
            return (
              <li key={it.id} className="track-row">
                <span className="index">{i + 1}</span>
                {it.track.thumbnailUrl && <img className="thumb small" src={it.track.thumbnailUrl} alt="" />}
                <div className="track-meta">
                  <a className="title" href={it.track.url} target="_blank" rel="noreferrer">{it.track.title}</a>
                  <div className="muted small">{it.track.artist ?? it.track.provider} · {it.addedByName}{mine ? " (you)" : ""}</div>
                </div>
                <div className="row actions">
                  {canReorder && (
                    <>
                      <button className="btn icon" title="Move up" disabled={i === 0} onClick={() => move(it.id, i - 1)}>↑</button>
                      <button className="btn icon" title="Move down" disabled={i === queue.items.length - 1} onClick={() => move(it.id, i + 1)}>↓</button>
                    </>
                  )}
                  {removable && <button className="btn icon danger" title="Remove" onClick={() => remove(it.id)}>✕</button>}
                </div>
              </li>
            );
          })}
        </ol>
      )}
    </section>
  );
}
