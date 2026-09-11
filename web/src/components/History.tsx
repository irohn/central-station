import { useEffect, useState } from "react";
import { api } from "../api";
import type { HistoryEntry } from "../types";

export function History({ version }: { version: string }) {
  const [entries, setEntries] = useState<HistoryEntry[]>([]);
  useEffect(() => {
    api<{ entries: HistoryEntry[] }>("GET", "/history?limit=100").then((r) => setEntries(r.entries)).catch(() => {});
  }, [version]);

  return (
    <section className="card">
      <h2>History</h2>
      {entries.length === 0 ? (
        <p className="muted">Nothing has played yet.</p>
      ) : (
        <ol className="queue">
          {entries.map((h) => (
            <li key={h.item.id + h.endedAt} className="track-row">
              {h.item.track.thumbnailUrl && <img className="thumb small" src={h.item.track.thumbnailUrl} alt="" />}
              <div className="track-meta">
                <a className="title" href={h.item.track.url} target="_blank" rel="noreferrer">{h.item.track.title}</a>
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
    </section>
  );
}
