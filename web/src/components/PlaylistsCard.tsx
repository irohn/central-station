import { useState } from "react";
import { api, ApiError } from "../api";
import type { QueuedPlaylist, QueueState, User } from "../types";
import { ListMusic, Shuffle, X } from "./Icons";

/**
 * Playlists card: user-queued playlists in play order (each plays through once,
 * after the song queue), followed by the default playlist that loops when
 * nothing else is left.
 */
export function PlaylistsCard({ queue, user, onManage }: { queue: QueueState; user: User; onManage: () => void }) {
  const [error, setError] = useState<string | null>(null);
  const fb = queue.fallback;
  const canManageFallback = user.permissions.includes("fallback:manage");
  const canWrite = user.permissions.includes("playlist:write");
  const canAny = user.permissions.includes("playlist:remove_any");
  const mayEdit = (p: QueuedPlaylist) => canAny || (canWrite && p.addedById === user.id);
  const lists = queue.playlists ?? [];
  const showFallback = !!fb?.enabled && !!fb.next;

  if (lists.length === 0 && !showFallback && !canManageFallback) return null;

  async function toggleShuffle(p: QueuedPlaylist) {
    setError(null);
    try {
      await api("PATCH", `/queue/playlists/${p.id}`, { shuffle: !p.shuffle });
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    }
  }
  async function remove(p: QueuedPlaylist) {
    setError(null);
    try {
      await api("DELETE", `/queue/playlists/${p.id}`);
    } catch (e) {
      setError(e instanceof ApiError ? e.message : String(e));
    }
  }

  const songsWaiting = queue.items.length > 0 || (!!queue.current && !queue.current.source);

  return (
    <section className="card playlists-card">
      <div className="row space-between">
        <h2>Playlists</h2>
        <span className="muted small">{lists.length === 0 ? "none queued" : `${lists.length} queued`}</span>
      </div>
      {error && <p className="error small">{error}</p>}

      {lists.length > 0 && (
        <ol className="queue">
          {lists.map((p, i) => (
            <li key={p.id} className={`playlist-row${p.playing ? " is-playing" : ""}`}>
              <div className="track-row">
                <span className="index">{i + 1}</span>
                {p.thumbnailUrl ? <img className="thumb small" src={p.thumbnailUrl} alt="" /> : <ListMusic className="muted" />}
                <div className="track-meta">
                  <div className="row wrap">
                    <a className="title" href={p.url} target="_blank" rel="noreferrer">{p.title}</a>
                    {p.playing && <span className="pill playing">playing</span>}
                  </div>
                  <div className="muted small">
                    {p.played}/{p.trackCount} played · {p.addedByName}{p.addedById === user.id ? " (you)" : ""}
                    {p.next && !p.playing && i === 0 && !songsWaiting ? " · up next" : ""}
                  </div>
                  {p.next && <div className="muted small ellipsis">Next: {p.next.title}</div>}
                </div>
                <div className="row actions">
                  <button
                    className={`btn icon shuffle-toggle${p.shuffle ? " on" : ""}`}
                    title={p.shuffle ? "Shuffle on" : "Shuffle off"}
                    aria-pressed={p.shuffle}
                    disabled={!mayEdit(p)}
                    onClick={() => toggleShuffle(p)}
                  >
                    <Shuffle width={16} height={16} />
                  </button>
                  {mayEdit(p) && (
                    <button className="btn icon danger" title={p.playing ? "Skip this playlist" : "Remove playlist"} onClick={() => remove(p)}>
                      <X width={16} height={16} />
                    </button>
                  )}
                </div>
              </div>
            </li>
          ))}
        </ol>
      )}

      <div className={`fallback-row${lists.length > 0 ? " after-list" : ""}`}>
        {showFallback && fb ? (
          <div className="track-row">
            {fb.next?.thumbnailUrl ? <img className="thumb small" src={fb.next.thumbnailUrl} alt="" /> : <ListMusic className="muted" />}
            <div className="track-meta">
              <div className="row wrap">
                <span className="strong">Default playlist</span>
                {fb.shuffle && <span className="pill icon-pill" title="Shuffle on"><Shuffle width={12} height={12} /> shuffle</span>}
                {queue.current?.source === "fallback" && <span className="pill playing">playing</span>}
              </div>
              <div className="muted small ellipsis">
                {fb.playlistUrl ? <a href={fb.playlistUrl} target="_blank" rel="noreferrer">{fb.playlistTitle}</a> : fb.playlistTitle} · {fb.trackCount} tracks · loops when nothing else is queued
              </div>
              {fb.next && <div className="muted small ellipsis">Next: {fb.next.title}</div>}
            </div>
            {canManageFallback && <button className="btn link" onClick={onManage}>Manage</button>}
          </div>
        ) : canManageFallback ? (
          <div className="track-row">
            <ListMusic className="muted" />
            <div className="track-meta">
              <div className="strong">Default playlist is off</div>
              <div className="muted small">Pick a YouTube playlist to keep the music going when nothing is queued.</div>
            </div>
            <button className="btn" onClick={onManage}>Set up</button>
          </div>
        ) : null}
      </div>
    </section>
  );
}
