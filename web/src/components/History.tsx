import { useEffect, useState } from "react";
import { ApiError } from "../api";
import type { HistoryEntry, Track, User } from "../types";
import { useZone } from "../zone";
import { TrackMenu } from "./TrackMenu";
import type { MenuItem } from "./TrackMenu";

export function History({ version, user }: { version: string; user: User }) {
  const { zoneId, zapi } = useZone();
  const [entries, setEntries] = useState<HistoryEntry[]>([]);
  const [menu, setMenu] = useState<{ x: number; y: number; track: Track } | null>(null);
  const [notice, setNotice] = useState<{ ok?: string; error?: string } | null>(null);
  useEffect(() => {
    zapi<{ entries: HistoryEntry[] }>("GET", "/history?limit=100").then((r) => setEntries(r.entries)).catch(() => {});
  }, [version, zoneId, zapi]);

  const canAdd = user.permissions.includes("queue:add");
  const canNext = user.permissions.includes("queue:reorder");
  const canNow = user.permissions.includes("playback:control");

  async function queue(track: Track, mode: "now" | "next" | "add") {
    setNotice(null);
    try {
      await zapi("POST", "/queue", { input: track.url, playNow: mode === "now", playNext: mode === "next" });
      setNotice({ ok: mode === "now" ? `Playing now: ${track.title}` : mode === "next" ? `Playing next: ${track.title}` : `Queued: ${track.title}` });
    } catch (e) {
      setNotice({ error: e instanceof ApiError ? e.message : String(e) });
    }
  }

  const open = (e: React.MouseEvent, track: Track) => {
    e.preventDefault();
    setMenu({ x: e.clientX, y: e.clientY, track });
  };

  const items = (track: Track): MenuItem[] => [
    ...(canNow ? [{ label: "Play now", onSelect: () => queue(track, "now") }] : []),
    ...(canNext ? [{ label: "Play next", onSelect: () => queue(track, "next") }] : []),
    ...(canAdd ? [{ label: "Add to queue", onSelect: () => queue(track, "add") }] : []),
    { label: "Open link", onSelect: () => window.open(track.url, "_blank", "noopener,noreferrer") },
  ];

  return (
    <section className="card">
      <h2>History</h2>
      {notice?.ok && <p className="ok small">{notice.ok}</p>}
      {notice?.error && <p className="error small">{notice.error}</p>}
      {entries.length === 0 ? (
        <p className="muted">Nothing has played yet.</p>
      ) : (
        <ol className="queue">
          {entries.map((h) => (
            <li
              key={h.item.id + h.endedAt}
              className="track-row clickable"
              role="button"
              tabIndex={0}
              onClick={(e) => open(e, h.item.track)}
              onContextMenu={(e) => open(e, h.item.track)}
              onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); const r = (e.currentTarget as HTMLElement).getBoundingClientRect(); setMenu({ x: r.left + 48, y: r.bottom - 8, track: h.item.track }); } }}
            >
              {h.item.track.thumbnailUrl && <img className="thumb small" src={h.item.track.thumbnailUrl} alt="" />}
              <div className="track-meta">
                <span className="title">{h.item.track.title}</span>
                <div className="muted small">
                  {h.item.track.artist ?? h.item.track.provider} · {h.item.source === "fallback" ? `default playlist (${h.item.addedByName})` : h.item.source === "playlist" ? `${h.item.addedByName} · playlist ${h.item.playlistTitle ?? ""}` : h.item.addedByName} · {new Date(h.endedAt).toLocaleString()}
                </div>
                {h.error && <div className="warn small">{h.error}</div>}
              </div>
              <span className={`pill ${h.endReason}`}>{h.endReason}</span>
            </li>
          ))}
        </ol>
      )}
      {menu && <TrackMenu x={menu.x} y={menu.y} items={items(menu.track)} onClose={() => setMenu(null)} />}
    </section>
  );
}
