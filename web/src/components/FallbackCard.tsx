import type { QueueState, User } from "../types";
import { ListMusic, Shuffle } from "./Icons";

/**
 * Compact "default playlist" card shown under the users' queue. It tells
 * people what plays when the queue runs dry without mixing those tracks into
 * the queue itself.
 */
export function FallbackCard({ queue, user, onManage }: { queue: QueueState; user: User; onManage: () => void }) {
  const fb = queue.fallback;
  const canManage = user.permissions.includes("fallback:manage");

  if (!fb?.enabled || !fb.next) {
    if (!canManage) return null;
    return (
      <section className="card fallback-card off">
        <div className="fallback-head">
          <ListMusic className="muted" />
          <div className="track-meta">
            <div className="strong">Default playlist is off</div>
            <div className="muted small">Pick a YouTube playlist to keep the music going when the queue is empty.</div>
          </div>
          <button className="btn" onClick={onManage}>Set up</button>
        </div>
      </section>
    );
  }

  const playingFromPlaylist = queue.current?.source === "fallback";
  const waiting = queue.items.length > 0 || (!!queue.current && !playingFromPlaylist);
  const t = fb.next;

  return (
    <section className="card fallback-card">
      <div className="fallback-head">
        <ListMusic className="muted" />
        <div className="track-meta">
          <div className="row wrap">
            <span className="strong">Default playlist</span>
            {fb.shuffle && <span className="pill icon-pill" title="Shuffle on"><Shuffle width={12} height={12} /> shuffle</span>}
          </div>
          <div className="muted small ellipsis">
            {fb.playlistUrl ? <a href={fb.playlistUrl} target="_blank" rel="noreferrer">{fb.playlistTitle}</a> : fb.playlistTitle} · {fb.trackCount} tracks
          </div>
        </div>
        {canManage && <button className="btn link" onClick={onManage}>Manage</button>}
      </div>
      <div className="track-row fallback-next">
        {t.thumbnailUrl && <img className="thumb small" src={t.thumbnailUrl} alt="" />}
        <div className="track-meta">
          <div className="muted small label">{waiting ? "Resumes when the queue is empty" : "Up next from the playlist"}</div>
          <a className="title" href={t.url} target="_blank" rel="noreferrer">{t.title}</a>
          <div className="muted small">{t.artist ?? t.provider}</div>
        </div>
      </div>
    </section>
  );
}
