import { useState } from "react";
import { ApiError } from "../api";
import type { QueuedPlaylist, QueueItem, QueueState } from "../types";
import { useZone } from "../zone";

/** Inline "paste a link or search" form; lives in the Up next header. */
export function AddForm({ onAdded, onResult }: { onAdded: (q: QueueState) => void; onResult: (msg: { ok?: string; error?: string } | null) => void }) {
  const { zapi } = useZone();
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!input.trim()) return;
    setBusy(true);
    onResult(null);
    try {
      const res = await zapi<{ kind: "track" | "playlist"; item?: QueueItem; playlist?: QueuedPlaylist; queue: QueueState }>("POST", "/queue", { input: input.trim() });
      onAdded(res.queue);
      onResult({
        ok: res.kind === "playlist" && res.playlist
          ? `Queued playlist: ${res.playlist.title} (${res.playlist.trackCount} tracks, plays after the songs above)`
          : `Queued: ${res.item?.track.title ?? "track"}`,
      });
      setInput("");
    } catch (err) {
      onResult({ error: err instanceof ApiError ? err.message : String(err) });
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="add-inline" onSubmit={submit}>
      <input
        aria-label="Add to queue"
        placeholder="Paste a YouTube link, a playlist, or search…"
        value={input}
        onChange={(e) => setInput(e.target.value)}
        disabled={busy}
        autoComplete="off"
        enterKeyHint="done"
      />
      <button className="btn primary" type="submit" disabled={busy || !input.trim()}>
        {busy ? "Adding…" : "Add"}
      </button>
    </form>
  );
}
