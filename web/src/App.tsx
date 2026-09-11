import { useEffect, useState } from "react";
import { api, ApiError } from "./api";
import type { User } from "./types";
import { useLive } from "./useLive";
import { Login } from "./components/Login";
import { NowPlaying } from "./components/NowPlaying";
import { AddForm } from "./components/AddForm";
import { QueueList } from "./components/QueueList";
import { History } from "./components/History";
import { Users } from "./components/Users";
import { ThemeToggle } from "./components/ThemeToggle";
import { useTheme } from "./theme";

type Tab = "queue" | "history" | "users";

export default function App() {
  const [user, setUser] = useState<User | null | undefined>(undefined);
  const [tab, setTab] = useState<Tab>("queue");
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

  return (
    <div className="app">
      <header className="topbar">
        <div className="brand">Central Station</div>
        <nav className="tabs">
          <button className={tab === "queue" ? "active" : ""} onClick={() => setTab("queue")}>Queue</button>
          <button className={tab === "history" ? "active" : ""} onClick={() => setTab("history")}>History</button>
          {isAdmin && <button className={tab === "users" ? "active" : ""} onClick={() => setTab("users")}>Users</button>}
        </nav>
        <div className="status">
          <span className={`dot ${player.online ? "on" : "off"}`} title={player.online ? `Player online: ${player.name ?? ""}` : "No player connected"} />
          <span className="muted small">{player.online ? player.name ?? "player" : "no player"}</span>
          {!connected && <span className="muted small"> · reconnecting…</span>}
        </div>
        <div className="user">
          {user.pictureUrl && <img src={user.pictureUrl} alt="" className="avatar" />}
          <span>{user.name}</span>
          <span className="pill role">{user.role}</span>
          <button className="btn link" onClick={logout}>Sign out</button>
        </div>
        <ThemeToggle theme={theme} onToggle={toggleTheme} />
      </header>

      <main>
        {tab === "queue" && queue && (
          <>
            <NowPlaying queue={queue} player={player} user={user} />
            {user.permissions.includes("queue:add") ? (
              <AddForm onAdded={setQueue} />
            ) : (
              <p className="muted small">You are a listener. Ask a manager for collaborator access to add songs.</p>
            )}
            <QueueList queue={queue} user={user} />
          </>
        )}
        {tab === "queue" && !queue && <div className="center muted">Loading queue…</div>}
        {tab === "history" && <History version={queue?.updatedAt ?? ""} />}
        {tab === "users" && isAdmin && <Users me={user} />}
      </main>
    </div>
  );
}
