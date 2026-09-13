import { useCallback, useEffect, useState } from "react";
import { api, ApiError } from "./api";
import type { User, ZoneSummary } from "./types";
import { useLive } from "./useLive";
import { ZoneProvider, readStoredZone, storeZone } from "./zone";
import { ZoneSwitcher } from "./components/ZoneSwitcher";
import { ZonesPage } from "./components/ZonesPage";
import { Login } from "./components/Login";
import { NowPlaying } from "./components/NowPlaying";
import { QueueList } from "./components/QueueList";
import { PlaylistsCard } from "./components/PlaylistsCard";
import { FallbackSettings } from "./components/FallbackSettings";
import { History } from "./components/History";
import { Users } from "./components/Users";
import { SettingsMenu } from "./components/SettingsMenu";
import { Broadcast } from "./components/Broadcast";
import { Alerts } from "./components/Alerts";
import { Account } from "./components/Account";
import type { AuthMethods } from "./types";
import { ChevronLeft, Gear, Megaphone } from "./components/Icons";
import { ThemeToggle } from "./components/ThemeToggle";
import { useTheme } from "./theme";

type View = "queue" | "history" | "users" | "fallback" | "broadcast" | "alerts" | "account" | "zones";

const subpageTitles: Record<"users" | "fallback" | "broadcast" | "alerts" | "account" | "zones", string> = { users: "Users", fallback: "Default playlist", broadcast: "Broadcast", alerts: "Alerts", account: "Account", zones: "Zones" };

export default function App() {
  const [user, setUser] = useState<User | null | undefined>(undefined);
  const [requestedView, setView] = useState<View>("queue");
  const [zones, setZones] = useState<ZoneSummary[] | null>(null);
  const [wantedZone, setWantedZone] = useState<string>(readStoredZone);
  // The zone shown: the remembered one if it still exists, else the default, else the first.
  const zoneId = zones?.find((z) => z.id === wantedZone)?.id ?? zones?.find((z) => z.isDefault)?.id ?? zones?.[0]?.id ?? "";
  const zone = zones?.find((z) => z.id === zoneId);
  const onZones = useCallback((z: ZoneSummary[]) => setZones(z), []);
  const { queue, player, connected, setQueue } = useLive(!!user && !!zoneId, zoneId, onZones);
  const selectZone = (id: string) => { setWantedZone(id); storeZone(id); };
  const [theme, toggleTheme] = useTheme();
  const [methods, setMethods] = useState<AuthMethods | null>(null);
  const [resent, setResent] = useState<string | null>(null);
  // Per-page settings panel (gear in the page header); closes when the page changes.
  const [settingsOpen, setSettingsOpen] = useState(false);
  const go = (v: View) => { setSettingsOpen(false); setView(v); };

  useEffect(() => {
    api<User>("GET", "/me")
      .then(setUser)
      .catch((e) => {
        if (e instanceof ApiError && e.status === 401) setUser(null);
        else setUser(null);
      });
    api<AuthMethods>("GET", "/auth/methods").then(setMethods).catch(() => {});
  }, []);

  useEffect(() => {
    if (!user) return;
    api<{ zones: ZoneSummary[] }>("GET", "/zones").then((r) => setZones(r.zones)).catch(() => setZones([]));
  }, [user]);

  async function resendVerification() {
    try {
      await api("POST", "/auth/verify/resend");
      setResent("Verification email sent. Check your inbox.");
    } catch (e) {
      setResent(e instanceof ApiError ? e.message : String(e));
    }
  }

  async function logout() {
    await api("POST", "/auth/logout").catch(() => {});
    setUser(null);
    setZones(null);
    setView("queue");
  }

  if (user === undefined) return <div className="center muted">Loading…</div>;
  if (user === null) {
    return (
      <>
        <div className="floating-toggle"><ThemeToggle theme={theme} onToggle={toggleTheme} /></div>
        <Login onLogin={setUser} />
      </>
    );
  }

  const canManageFallback = user.permissions.includes("fallback:manage");
  const canBroadcast = user.permissions.includes("broadcast:send");
  const canAlerts = user.permissions.includes("alerts:manage");
  const canZones = user.permissions.includes("integrations:manage") || user.permissions.includes("output:control");
  // Fall back to the queue when the role cannot use the requested page (e.g. after a role change).
  const view: View =
    (requestedView === "fallback" && !canManageFallback) || (requestedView === "broadcast" && !canBroadcast) || (requestedView === "alerts" && !canAlerts) || (requestedView === "zones" && !canZones)
      ? "queue" : requestedView;
  const subpage = view !== "queue" && view !== "history";
  // Pages that have a settings panel behind the gear in their header.
  const hasSettings = view === "broadcast" && user.permissions.includes("settings:manage");

  if (!zones) return <div className="center muted">Loading…</div>;
  if (!zoneId) return <div className="center muted">No zones are configured. Ask an admin to add one.</div>;

  return (
    <ZoneProvider zoneId={zoneId}>
    <div className="app">
      <header className="topbar">
        <nav className="tabs" aria-label="Main">
          <button className={view === "queue" ? "active" : ""} onClick={() => go("queue")}>Queue</button>
          <button className={view === "history" ? "active" : ""} onClick={() => go("history")}>History</button>
        </nav>
        <ZoneSwitcher zones={zones} zoneId={zoneId} onSelect={selectZone} onManage={canZones ? () => go("zones") : undefined} />
        <div className="topbar-right">
          <button className="user user-link" type="button" title={`${user.name} · ${user.role} · open Users`} onClick={() => go("users")}>
            {user.pictureUrl ? <img src={user.pictureUrl} alt="" className="avatar" /> : <span className="avatar initial">{user.name.slice(0, 1)}</span>}
            <span className="user-name">{user.name}</span>
          </button>
          <SettingsMenu
            user={user}
            player={player}
            zoneName={zone?.name ?? "Zone"}
            connected={connected}
            theme={theme}
            onToggleTheme={toggleTheme}
            onNavigate={go}
            onLogout={logout}
          />
        </div>
      </header>

      {!connected && <div className="banner warn-banner">Connection lost, reconnecting…</div>}
      {user.emailVerified === false && (
        <div className="banner warn-banner verify-banner">
          <span>
            Your email isn't verified yet, so you can listen but not add songs.{" "}
            {methods?.passwordVerification === "email" ? "Open the link we emailed you" : "Ask an admin to confirm your account"}.
          </span>
          {methods?.passwordVerification === "email" && <button className="btn link" onClick={resendVerification}>Resend email</button>}
          {resent && <span className="small">{resent}</span>}
        </div>
      )}
      {player.broadcast && (
        <div className="banner broadcast-banner"><Megaphone width={18} height={18} /> Announcement: <strong>{player.broadcast.clipName}</strong> · {player.broadcast.byName}{player.broadcast.loops > 1 ? ` · ${player.broadcast.loops}×` : ""}{player.broadcastsQueued > 0 ? ` · ${player.broadcastsQueued} more waiting` : ""}</div>
      )}

      <main>
        {subpage && (
          <div className="subpage-head">
            <button className="btn icon back" onClick={() => go("queue")} aria-label="Back to queue"><ChevronLeft /></button>
            <h1>{subpageTitles[view]}{view === "fallback" || view === "broadcast" ? <span className="muted"> · {zone?.name}</span> : null}</h1>
            {hasSettings && (
              <button className={`btn icon page-settings${settingsOpen ? " active" : ""}`} type="button" title="Page settings" aria-label="Page settings" aria-pressed={settingsOpen} onClick={() => setSettingsOpen((o) => !o)}>
                <Gear width={18} height={18} />
              </button>
            )}
          </div>
        )}

        {view === "queue" && queue && (
          <>
            <NowPlaying queue={queue} player={player} user={user} />
            <QueueList queue={queue} user={user} onAdded={setQueue} />
            <PlaylistsCard queue={queue} user={user} onManage={() => go("fallback")} />
          </>
        )}
        {view === "queue" && !queue && <div className="center muted">Loading queue…</div>}
        {view === "history" && <History version={queue?.updatedAt ?? ""} user={user} />}
        {view === "users" && <Users me={user} onMeChanged={setUser} />}
        {view === "fallback" && canManageFallback && <FallbackSettings user={user} />}
        {view === "broadcast" && canBroadcast && <Broadcast user={user} player={player} zones={zones} settingsOpen={settingsOpen} onCloseSettings={() => setSettingsOpen(false)} />}
        {view === "alerts" && canAlerts && <Alerts zones={zones} zoneId={zoneId} />}
        {view === "zones" && canZones && <ZonesPage zones={zones} zoneId={zoneId} user={user} onSelect={selectZone} />}
        {view === "account" && <Account user={user} />}
      </main>
    </div>
    </ZoneProvider>
  );
}
