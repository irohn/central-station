import { useState } from "react";
import { api, ApiError } from "../api";
import type { QueueItem, QueueState } from "../types";

/** Inline "paste a link or search" form; lives in the Up next header. */
export function AddForm({ onAdded, onResult }: { onAdded: (q: QueueState) => void; onResult: (msg: { ok?: string; error?: string } | null) => void }) {
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!input.trim()) return;
    setBusy(true);
    onResult(null);
    try {
      const res = await api<{ item: QueueItem; queue: QueueState }>("POST", "/queue", { input: input.trim() });
      onAdded(res.queue);
      onResult({ ok: `Queued: ${res.item.track.title}` });
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
        placeholder="Paste a YouTube link or search…"
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
