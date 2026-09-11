import { useEffect, useRef, useState } from "react";
import { api, eventsWsUrl } from "./api";
import type { LiveEvent, PlayerStatus, QueueState } from "./types";

const emptyPlayer: PlayerStatus = { online: false, positionSeconds: 0, durationSeconds: 0 };

/** Keeps queue + player state in sync over the events WebSocket, with reconnect. */
export function useLive(enabled: boolean) {
  const [queue, setQueue] = useState<QueueState | null>(null);
  const [player, setPlayer] = useState<PlayerStatus>(emptyPlayer);
  const [connected, setConnected] = useState(false);
  const wsRef = useRef<WebSocket | null>(null);

  useEffect(() => {
    if (!enabled) return;
    let closed = false;
    let backoff = 1000;
    let timer: number | undefined;

    const refresh = () => {
      api<QueueState>("GET", "/queue").then(setQueue).catch(() => {});
      api<PlayerStatus>("GET", "/player").then(setPlayer).catch(() => {});
    };
    refresh();

    const connect = () => {
      if (closed) return;
      const ws = new WebSocket(eventsWsUrl());
      wsRef.current = ws;
      ws.onopen = () => {
        setConnected(true);
        backoff = 1000;
      };
      ws.onmessage = (m) => {
        let ev: LiveEvent;
        try {
          ev = JSON.parse(m.data);
        } catch {
          return;
        }
        if (ev.queue) setQueue(ev.queue);
        if (ev.player) setPlayer(ev.player);
      };
      ws.onclose = () => {
        setConnected(false);
        if (closed) return;
        timer = window.setTimeout(() => {
          refresh();
          connect();
        }, backoff);
        backoff = Math.min(backoff * 2, 15000);
      };
      ws.onerror = () => ws.close();
    };
    connect();

    return () => {
      closed = true;
      if (timer) clearTimeout(timer);
      wsRef.current?.close();
    };
  }, [enabled]);

  return { queue, player, connected, setQueue };
}
