import { createContext, useContext, useMemo } from "react";
import type { ReactNode } from "react";
import { api } from "./api";

/** The zone the UI is looking at, plus an API client scoped to it. */
export interface ZoneCtx {
  zoneId: string;
  zapi: <T>(method: string, path: string, body?: unknown) => Promise<T>;
}

const Ctx = createContext<ZoneCtx | null>(null);

export function ZoneProvider({ zoneId, children }: { zoneId: string; children: ReactNode }) {
  const value = useMemo<ZoneCtx>(() => ({
    zoneId,
    zapi: <T,>(method: string, path: string, body?: unknown) => api<T>(method, `/zones/${zoneId}${path}`, body),
  }), [zoneId]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useZone(): ZoneCtx {
  const c = useContext(Ctx);
  if (!c) throw new Error("useZone must be used inside ZoneProvider");
  return c;
}

const KEY = "cs-zone";

export function readStoredZone(): string {
  try {
    return localStorage.getItem(KEY) ?? "";
  } catch {
    return "";
  }
}

export function storeZone(id: string) {
  try {
    localStorage.setItem(KEY, id);
  } catch {
    /* ignore */
  }
}
