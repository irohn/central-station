import type { Theme } from "../theme";
import { Moon, Sun } from "./Icons";

export function ThemeToggle({ theme, onToggle }: { theme: Theme; onToggle: () => void }) {
  const next = theme === "light" ? "dark" : "light";
  return (
    <button className="btn icon theme-toggle" type="button" onClick={onToggle} title={`Switch to ${next} mode`} aria-label={`Switch to ${next} mode`}>
      {theme === "light" ? <Moon /> : <Sun />}
    </button>
  );
}
