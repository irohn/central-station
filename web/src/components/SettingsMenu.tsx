import { useEffect, useState } from "react";
import { createPortal } from "react-dom";
import type { PlayerStatus, User } from "../types";
import type { Theme } from "../theme";
import { Clock, Gear, KeyIcon, ListMusic, LogOut, Megaphone, Moon, Radio, Speaker, Sun, UsersIcon, X } from "./Icons";

export type MenuView = "fallback" | "users" | "broadcast" | "alerts" | "account" | "zones";

export function SettingsMenu({ user, player, zoneName, connected, theme, onToggleTheme, onNavigate, onLogout }: {
  user: User;
  player: PlayerStatus;
  zoneName: string;
  connected: boolean;
  theme: Theme;
  onToggleTheme: () => void;
  onNavigate: (v: MenuView) => void;
  onLogout: () => void;
}) {
  const [open, setOpen] = useState(false);
  const canManageFallback = user.permissions.includes("fallback:manage");
  const canBroadcast = user.permissions.includes("broadcast:send");
  const canAlerts = user.permissions.includes("alerts:manage");
  const isAdmin = user.permissions.includes("users:manage");
  const canZones = user.permissions.includes("integrations:manage") || user.permissions.includes("output:control");

  useEffect(() => {
    if (!open) return;
    const onKey = (e: KeyboardEvent) => { if (e.key === "Escape") setOpen(false); };
    document.addEventListener("keydown", onKey);
    // Keep the page from scrolling behind the sheet on phones.
    const prev = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    return () => {
      document.removeEventListener("keydown", onKey);
      document.body.style.overflow = prev;
    };
  }, [open]);

  const go = (v: MenuView) => { setOpen(false); onNavigate(v); };
  const health = !connected ? "warn" : player.online ? "on" : "off";
  const healthText = !connected
    ? "Reconnecting to server…"
    : player.online ? `${zoneName}: player online${player.name ? ` · ${player.name}` : ""}` : `${zoneName}: no player connected`;

  return (
    <div className="menu-root">
      <button
        className="btn icon gear"
        type="button"
        aria-haspopup="menu"
        aria-expanded={open}
        aria-label="Settings and status"
        title={healthText}
        onClick={() => setOpen((o) => !o)}
      >
        <Gear />
        <span className={`dot badge ${health}`} />
      </button>

      {/* Rendered on <body>: inside the sticky top bar, Safari would anchor the
          fixed bottom sheet to the bar (backdrop-filter creates a containing block). */}
      {open && createPortal(
        <div className="menu-layer">
          <div className="menu-backdrop" onClick={() => setOpen(false)} />
          <div className="menu" role="menu">
            <div className="menu-user">
              {user.pictureUrl ? <img src={user.pictureUrl} alt="" className="avatar lg" /> : <span className="avatar lg initial">{user.name.slice(0, 1)}</span>}
              <div className="menu-user-text">
                <div className="strong ellipsis">{user.name}</div>
                <div className="muted small ellipsis">{user.email}</div>
              </div>
              <span className="pill role">{user.role}</span>
              <button className="btn icon menu-close" type="button" aria-label="Close menu" onClick={() => setOpen(false)}><X /></button>
            </div>

            <div className="menu-status">
              <span className={`dot ${health}`} />
              <div className="menu-status-text">
                <div>{healthText}</div>
                {player.lastError && <div className="warn small">Last error: {player.lastError}</div>}
                {!player.online && connected && <div className="muted small">Nothing comes out of the speakers until it connects.</div>}
              </div>
              <Radio className="muted" />
            </div>

            {(canManageFallback || isAdmin || canBroadcast || canAlerts || canZones) && (
              <div className="menu-section">
                {canBroadcast && (
                  <button className="menu-item" role="menuitem" onClick={() => go("broadcast")}>
                    <Megaphone /> <span>Broadcast</span>
                  </button>
                )}
                {canAlerts && (
                  <button className="menu-item" role="menuitem" onClick={() => go("alerts")}>
                    <Clock /> <span>Alerts</span>
                  </button>
                )}
                {canManageFallback && (
                  <button className="menu-item" role="menuitem" onClick={() => go("fallback")}>
                    <ListMusic /> <span>Default playlist</span>
                  </button>
                )}
                {isAdmin && (
                  <button className="menu-item" role="menuitem" onClick={() => go("users")}>
                    <UsersIcon /> <span>Users</span>
                  </button>
                )}
                {canZones && (
                  <button className="menu-item" role="menuitem" onClick={() => go("zones")}>
                    <Speaker /> <span>Zones</span>
                  </button>
                )}
              </div>
            )}

            <div className="menu-section">
              {user.hasPassword && (
                <button className="menu-item" role="menuitem" onClick={() => go("account")}>
                  <KeyIcon /> <span>Change password</span>
                </button>
              )}
              <button className="menu-item" role="menuitem" onClick={onToggleTheme}>
                {theme === "light" ? <Moon /> : <Sun />} <span>{theme === "light" ? "Dark mode" : "Light mode"}</span>
              </button>
              <button className="menu-item" role="menuitem" onClick={() => { setOpen(false); onLogout(); }}>
                <LogOut /> <span>Sign out</span>
              </button>
            </div>
          </div>
        </div>,
        document.body,
      )}
    </div>
  );
}
