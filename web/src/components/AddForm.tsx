import { useState } from "react";
import { api, ApiError } from "../api";
import type { QueueItem, QueueState } from "../types";

export function AddForm({ onAdded }: { onAdded: (q: QueueState) => void }) {
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [added, setAdded] = useState<string | null>(null);

  async function submit(e: React.FormEvent) {
    e.preventDefault();
    if (!input.trim()) return;
    setBusy(true);
    setError(null);
    setAdded(null);
    try {
      const res = await api<{ item: QueueItem; queue: QueueState }>("POST", "/queue", { input: input.trim() });
      onAdded(res.queue);
      setAdded(res.item.track.title);
      setInput("");
    } catch (err) {
      setError(err instanceof ApiError ? err.message : String(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <form className="card add-form" onSubmit={submit}>
      <label htmlFor="add-input">Add to queue</label>
      <div className="row">
        <input
          id="add-input"
          placeholder="Paste a YouTube link or type a search…"
          value={input}
          onChange={(e) => setInput(e.target.value)}
          disabled={busy}
          autoComplete="off"
        />
        <button className="btn primary" type="submit" disabled={busy || !input.trim()}>
          {busy ? "Adding…" : "Add"}
        </button>
      </div>
      {error && <p className="error">{error}</p>}
      {added && <p className="ok small">Queued: {added}</p>}
    </form>
  );
}
