import { useState } from "react";
import { api, ApiError } from "../api";
import type { QueueState, User } from "../types";
import { AddForm } from "./AddForm";

export function QueueList({ queue, user, onAdded }: { queue: QueueState; user: User; onAdded: (q: QueueState) => void }) {
  const [error, setError] = useState<string | null>(null);
  const [addResult, setAddResult] = useState<{ ok?: string; error?: string } | null>(null);
  const canAdd = user.permissions.includes("queue:add");
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
      <div className="queue-head">
        <h2>Up next</h2>
        {canAdd && <AddForm onAdded={onAdded} onResult={setAddResult} />}
        <span className="muted small queued-count">{queue.items.length} queued</span>
      </div>
      {!canAdd && <p className="muted small">You are a listener. Ask a manager for collaborator access to add songs.</p>}
      {addResult?.ok && <p className="ok small">{addResult.ok}</p>}
      {addResult?.error && <p className="error small">{addResult.error}</p>}
      {error && <p className="error">{error}</p>}
      {queue.items.length === 0 ? (
        <p className="muted">
          {(queue.playlists?.length ?? 0) > 0
            ? "No single songs queued. Add one and it plays right after the current track, before the playlists below."
            : queue.fallback?.enabled && queue.fallback.next
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
