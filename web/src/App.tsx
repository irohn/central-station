import { useEffect, useState } from "react";
import { api, ApiError } from "./api";
import type { User } from "./types";
import { useLive } from "./useLive";
import { Login } from "./components/Login";
import { NowPlaying } from "./components/NowPlaying";
import { QueueList } from "./components/QueueList";
import { FallbackCard } from "./components/FallbackCard";
import { FallbackSettings } from "./components/FallbackSettings";
import { History } from "./components/History";
import { Users } from "./components/Users";
import { SettingsMenu } from "./components/SettingsMenu";
import { ThemeToggle } from "./components/ThemeToggle";
import { ChevronLeft } from "./components/Icons";
import { useTheme } from "./theme";

type View = "queue" | "history" | "users" | "fallback";

const subpageTitles: Record<"users" | "fallback", string> = { users: "Users", fallback: "Default playlist" };

export default function App() {
  const [user, setUser] = useState<User | null | undefined>(undefined);
  const [requestedView, setView] = useState<View>("queue");
  const { queue, player, connected, setQueue } = useLive(!!user);
  const [theme, toggleTheme] = useTheme();

  useEffect(() => {
    api<User>("GET", "/me")
      .then(setUser)
      .catch((e) => {
        if (e instanceof ApiError && e.status === 401) setUser(null);
        else setUser(null);
      });
  }, []);

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
  // Fall back to the queue when the role cannot use the requested page (e.g. after a role change).
  const view: View =
    (requestedView === "users" && !isAdmin) || (requestedView === "fallback" && !canManageFallback) ? "queue" : requestedView;
  const subpage = view === "users" || view === "fallback";

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
            <FallbackCard queue={queue} user={user} onManage={() => setView("fallback")} />
          </>
        )}
        {view === "queue" && !queue && <div className="center muted">Loading queue…</div>}
        {view === "history" && <History version={queue?.updatedAt ?? ""} />}
        {view === "users" && isAdmin && <Users me={user} />}
        {view === "fallback" && canManageFallback && <FallbackSettings user={user} />}
      </main>
    </div>
  );
}
