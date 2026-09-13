import { useEffect, useState } from "react";
import { ApiError, fmtTime } from "../api";
import type { FallbackView, User } from "../types";
import { useZone } from "../zone";
import { Switch } from "./Switch";
import { ExternalLink, RefreshCw } from "./Icons";

function ago(iso: string): string {
  const s = Math.max(0, (Date.now() - new Date(iso).getTime()) / 1000);
  if (s < 60) return "just now";
  if (s < 3600) return `${Math.floor(s / 60)} min ago`;
  if (s < 86400) return `${Math.floor(s / 3600)} h ago`;
  return `${Math.floor(s / 86400)} d ago`;
}

export function FallbackSettings({ user }: { user: User }) {
  const { zoneId, zapi } = useZone();
  const canManage = user.permissions.includes("fallback:manage");
  const [view, setView] = useState<FallbackView | null>(null);
  const [url, setUrl] = useState("");
  const [enabled, setEnabled] = useState(false);
  const [shuffle, setShuffle] = useState(true);
  const [busy, setBusy] = useState<"save" | "refresh" | "reset" | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [ok, setOk] = useState<string | null>(null);
  const [showAll, setShowAll] = useState(false);

  const apply = (v: FallbackView) => {
    setView(v);
    setUrl(v.url);
    setEnabled(v.enabled);
    setShuffle(v.shuffle);
  };

  useEffect(() => {
    setView(null);
    zapi<FallbackView>("GET", "/settings/fallback").then(apply).catch((e) => setError(e instanceof ApiError ? e.message : String(e)));
  }, [zoneId, zapi]);

  const dirty = !!view && (url.trim() !== view.url || enabled !== view.enabled || shuffle !== view.shuffle);

  async function save(e: React.FormEvent) {
    e.preventDefault();
    setBusy("save");
    setError(null);
    setOk(null);
    try {
      const v = await zapi<FallbackView>("PUT", "/settings/fallback", { url: url.trim(), enabled, shuffle });
      apply(v);
      setOk(v.enabled && v.playlist ? `Saved. "${v.playlist.title}" plays whenever the queue is empty.` : "Saved.");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  }

  async function refresh() {
    setBusy("refresh");
    setError(null);
    setOk(null);
    try {
      const v = await zapi<FallbackView>("POST", "/settings/fallback/refresh");
      apply(v);
      setOk(`Playlist refreshed: ${v.playlist?.trackCount ?? 0} tracks.`);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  }

  async function reset() {
    if (!view?.envDefault) return;
    if (!window.confirm("Restore the default playlist configured on the server? The current playlist setting is replaced.")) return;
    setBusy("reset");
    setError(null);
    setOk(null);
    try {
      const v = await zapi<FallbackView>("POST", "/settings/fallback/reset");
      apply(v);
      setOk(v.playlist ? `Restored the default: "${v.playlist.title}".` : "Restored the default.");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(null);
    }
  }

  if (!view && !error) return <div className="center muted">Loading…</div>;
  const env = view?.envDefault;
  const isEnvDefault = !!env && !!view && view.url === env.url && view.enabled === env.enabled && view.shuffle === env.shuffle;

  const pl = view?.playlist;
  const tracks = pl?.tracks ?? [];
  const shown = showAll ? tracks : tracks.slice(0, 10);

  return (
    <>
      <form className="card" onSubmit={save}>
        <h2>Default playlist</h2>
        <p className="muted small">Plays automatically whenever nobody has queued anything. Songs people add always play right after the current track.</p>

        <label className="field">
          <span className="field-label">YouTube playlist link</span>
          <input
            type="url"
            inputMode="url"
            placeholder="https://www.youtube.com/playlist?list=…"
            value={url}
            onChange={(e) => setUrl(e.target.value)}
            disabled={!canManage || busy !== null}
            autoComplete="off"
          />
        </label>

        <Switch label="Enabled" hint="Turn off to leave the speakers silent when the queue is empty" checked={enabled} disabled={!canManage || busy !== null} onChange={setEnabled} />
        <Switch label="Shuffle" hint="Random order, no repeats until every track has played" checked={shuffle} disabled={!canManage || busy !== null} onChange={setShuffle} />

        {canManage && (
          <div className="row form-actions wrap">
            <button className="btn primary" type="submit" disabled={!dirty || busy !== null}>{busy === "save" ? "Saving…" : "Save"}</button>
            {dirty && <button className="btn link" type="button" disabled={busy !== null} onClick={() => view && apply(view)}>Discard</button>}
            {env && (
              <button className="btn" type="button" title={isEnvDefault ? "The server default is already in use" : `Restore ${env.url}`} disabled={busy !== null || isEnvDefault} onClick={reset}>
                {busy === "reset" ? "Restoring…" : "Reset to default"}
              </button>
            )}
          </div>
        )}
        {busy === "save" && <p className="muted small">Fetching the playlist from YouTube, this can take a few seconds…</p>}
        {error && <p className="error">{error}</p>}
        {ok && <p className="ok small">{ok}</p>}
      </form>

      {pl && (
        <section className="card">
          <div className="row space-between wrap">
            <div className="track-meta">
              <h2 className="ellipsis">
                <a href={pl.url} target="_blank" rel="noreferrer" className="inline-link">{pl.title} <ExternalLink width={14} height={14} /></a>
              </h2>
              <div className="muted small">{pl.trackCount} tracks · updated {ago(pl.fetchedAt)} · refreshes automatically every few hours</div>
            </div>
            {canManage && (
              <button className="btn with-icon" type="button" disabled={busy !== null} onClick={refresh}>
                <RefreshCw width={16} height={16} className={busy === "refresh" ? "spin" : ""} /> {busy === "refresh" ? "Refreshing…" : "Refresh"}
              </button>
            )}
          </div>
          {view?.next && <p className="muted small next-line">Up next: <strong>{view.next.title}</strong></p>}
          <ol className="queue tracklist">
            {shown.map((t, i) => (
              <li key={t.id} className={`track-row${view?.next?.id === t.id ? " is-next" : ""}`}>
                <span className="index">{i + 1}</span>
                {t.thumbnailUrl && <img className="thumb small" src={t.thumbnailUrl} alt="" />}
                <div className="track-meta">
                  <a className="title" href={t.url} target="_blank" rel="noreferrer">{t.title}</a>
                  <div className="muted small">{t.artist ?? t.provider}{t.durationSeconds ? ` · ${fmtTime(t.durationSeconds)}` : ""}</div>
                </div>
              </li>
            ))}
          </ol>
          {tracks.length > 10 && (
            <button className="btn link" type="button" onClick={() => setShowAll((s) => !s)}>
              {showAll ? "Show less" : `Show all ${tracks.length} tracks`}
            </button>
          )}
        </section>
      )}
    </>
  );
}
