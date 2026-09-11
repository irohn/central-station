import { useEffect, useState } from "react";
import { api, ApiError } from "./api";
import type { User } from "./types";
import { useLive } from "./useLive";
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
import { Megaphone } from "./components/Icons";
import { ThemeToggle } from "./components/ThemeToggle";
import { ChevronLeft } from "./components/Icons";
import { useTheme } from "./theme";

type View = "queue" | "history" | "users" | "fallback" | "broadcast" | "alerts" | "account";

const subpageTitles: Record<"users" | "fallback" | "broadcast" | "alerts" | "account", string> = { users: "Users", fallback: "Default playlist", broadcast: "Broadcast", alerts: "Alerts", account: "Account" };

export default function App() {
  const [user, setUser] = useState<User | null | undefined>(undefined);
  const [requestedView, setView] = useState<View>("queue");
  const { queue, player, connected, setQueue } = useLive(!!user);
  const [theme, toggleTheme] = useTheme();
  const [methods, setMethods] = useState<AuthMethods | null>(null);
  const [resent, setResent] = useState<string | null>(null);

  useEffect(() => {
    api<User>("GET", "/me")
      .then(setUser)
      .catch((e) => {
        if (e instanceof ApiError && e.status === 401) setUser(null);
        else setUser(null);
      });
    api<AuthMethods>("GET", "/auth/methods").then(setMethods).catch(() => {});
  }, []);

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

  const isAdmin = user.permissions.includes("users:manage");
  const canManageFallback = user.permissions.includes("fallback:manage");
  const canBroadcast = user.permissions.includes("broadcast:send");
  const canAlerts = user.permissions.includes("alerts:manage");
  // Fall back to the queue when the role cannot use the requested page (e.g. after a role change).
  const view: View =
    (requestedView === "users" && !isAdmin) || (requestedView === "fallback" && !canManageFallback) || (requestedView === "broadcast" && !canBroadcast) || (requestedView === "alerts" && !canAlerts)
      ? "queue" : requestedView;
  const subpage = view !== "queue" && view !== "history";

  return (
    <div className="app">
      <header className="topbar">
        <nav className="tabs" aria-label="Main">
          <button className={view === "queue" ? "active" : ""} onClick={() => setView("queue")}>Queue</button>
          <button className={view === "history" ? "active" : ""} onClick={() => setView("history")}>History</button>
        </nav>
        <div className="topbar-right">
          <div className="user" title={`${user.name} · ${user.role}`}>
            {user.pictureUrl ? <img src={user.pictureUrl} alt="" className="avatar" /> : <span className="avatar initial">{user.name.slice(0, 1)}</span>}
            <span className="user-name">{user.name}</span>
          </div>
          <SettingsMenu
            user={user}
            player={player}
            connected={connected}
            theme={theme}
            onToggleTheme={toggleTheme}
            onNavigate={setView}
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
            <button className="btn icon back" onClick={() => setView("queue")} aria-label="Back to queue"><ChevronLeft /></button>
            <h1>{subpageTitles[view]}</h1>
          </div>
        )}

        {view === "queue" && queue && (
          <>
            <NowPlaying queue={queue} player={player} user={user} />
            <QueueList queue={queue} user={user} onAdded={setQueue} />
            <PlaylistsCard queue={queue} user={user} onManage={() => setView("fallback")} />
          </>
        )}
        {view === "queue" && !queue && <div className="center muted">Loading queue…</div>}
        {view === "history" && <History version={queue?.updatedAt ?? ""} />}
        {view === "users" && isAdmin && <Users me={user} />}
        {view === "fallback" && canManageFallback && <FallbackSettings user={user} />}
        {view === "broadcast" && canBroadcast && <Broadcast user={user} player={player} />}
        {view === "alerts" && canAlerts && <Alerts player={player} />}
        {view === "account" && <Account user={user} />}
      </main>
    </div>
  );
}
