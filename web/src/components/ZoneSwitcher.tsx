import { useEffect, useRef, useState } from "react";
import type { ZoneSummary } from "../types";
import { Check, ChevronDown, Speaker } from "./Icons";

/** Top-bar picker for the zone the page shows. */
export function ZoneSwitcher({ zones, zoneId, onSelect, onManage }: {
  zones: ZoneSummary[];
  zoneId: string;
  onSelect: (id: string) => void;
  onManage?: () => void;
}) {
  const [open, setOpen] = useState(false);
  const root = useRef<HTMLDivElement>(null);
  const current = zones.find((z) => z.id === zoneId);

  useEffect(() => {
    if (!open) return;
    const onDoc = (e: MouseEvent) => { if (root.current && !root.current.contains(e.target as Node)) setOpen(false); };
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setOpen(false); };
    document.addEventListener("mousedown", onDoc);
    document.addEventListener("keydown", onKey);
    return () => { document.removeEventListener("mousedown", onDoc); document.removeEventListener("keydown", onKey); };
  }, [open]);

  if (zones.length <= 1 && !onManage) return null;

  return (
    <div className="zone-switcher" ref={root}>
      <button
        className="btn zone-button"
        type="button"
        aria-haspopup="listbox"
        aria-expanded={open}
        title={current ? `Zone: ${current.name}` : "Choose a zone"}
        onClick={() => setOpen((o) => !o)}
      >
        <span className={`dot ${current?.player.online ? "on" : "off"}`} />
        <span className="zone-name ellipsis">{current?.name ?? "Zone"}</span>
        <ChevronDown width={16} height={16} />
      </button>
      {open && (
        <div className="zone-menu" role="listbox">
          {zones.map((z) => (
            <button
              key={z.id}
              className={`zone-option${z.id === zoneId ? " selected" : ""}`}
              role="option"
              aria-selected={z.id === zoneId}
              onClick={() => { setOpen(false); onSelect(z.id); }}
            >
              <span className={`dot ${z.player.online ? "on" : "off"}`} />
              <span className="zone-option-text">
                <span className="strong ellipsis">{z.name}</span>
                <span className="muted small ellipsis">
                  {z.player.fault ? (z.player.fault.kind === "output" ? "Paused: no audio output" : "Paused: playback failing") : z.current ? `${z.status === "paused" ? "Paused: " : ""}${z.current.track.title}` : z.player.online ? "Idle" : "No player connected"}
                </span>
              </span>
              {z.id === zoneId && <Check width={16} height={16} className="muted" />}
            </button>
          ))}
          {onManage && (
            <button className="zone-option manage" onClick={() => { setOpen(false); onManage(); }}>
              <Speaker width={16} height={16} className="muted" /> <span>Manage zones…</span>
            </button>
          )}
        </div>
      )}
    </div>
  );
}
