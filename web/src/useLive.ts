import { useEffect, useRef, useState } from "react";
import { api, eventsWsUrl } from "./api";
import type { LiveEvent, PlayerStatus, QueueState, ZoneSummary } from "./types";

const emptyPlayer: PlayerStatus = { online: false, positionSeconds: 0, durationSeconds: 0, volume: 80, broadcastsQueued: 0 };

/**
 * Keeps one zone's queue + player state in sync over that zone's events
 * WebSocket, with reconnect. Zone summaries (every zone's status) ride on
 * the same socket and are handed to onZones.
 */
export function useLive(enabled: boolean, zoneId: string, onZones: (z: ZoneSummary[]) => void) {
  const [queue, setQueue] = useState<QueueState | null>(null);
  const [player, setPlayer] = useState<PlayerStatus>(emptyPlayer);
  const [connected, setConnected] = useState(false);
  const wsRef = useRef<WebSocket | null>(null);
  const onZonesRef = useRef(onZones);
  onZonesRef.current = onZones;

  useEffect(() => {
    setQueue(null);
    setPlayer(emptyPlayer);
    if (!enabled || !zoneId) return;
    let closed = false;
    let backoff = 1000;
    let timer: number | undefined;
    const base = `/zones/${zoneId}`;

    const refresh = () => {
      api<QueueState>("GET", `${base}/queue`).then((q) => { if (!closed) setQueue(q); }).catch(() => {});
      api<PlayerStatus>("GET", `${base}/player`).then((p) => { if (!closed) setPlayer(p); }).catch(() => {});
      api<{ zones: ZoneSummary[] }>("GET", "/zones").then((r) => { if (!closed) onZonesRef.current(r.zones); }).catch(() => {});
    };
    refresh();

    const connect = () => {
      if (closed) return;
      const ws = new WebSocket(eventsWsUrl(zoneId));
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
        if (ev.zones) onZonesRef.current(ev.zones);
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
  }, [enabled, zoneId]);

  return { queue, player, connected, setQueue };
}
