export type Role = "listener" | "collaborator" | "manager" | "admin";

export type Permission =
  | "queue:read" | "history:read" | "track:resolve"
  | "queue:add" | "queue:remove_own" | "playlist:write"
  | "queue:remove_any" | "queue:reorder" | "playback:control"
  | "settings:manage" | "users:manage" | "integrations:manage";

export interface User {
  id: string;
  email: string;
  name: string;
  pictureUrl?: string;
  role: Role;
  permissions: Permission[];
  createdAt: string;
  lastLoginAt: string;
}

export interface Track {
  provider: string;
  id: string;
  url: string;
  title: string;
  artist?: string;
  durationSeconds?: number;
  thumbnailUrl?: string;
}

export interface QueueItem {
  id: string;
  track: Track;
  addedById: string;
  addedByName: string;
  addedAt: string;
}

export type PlaybackStatus = "idle" | "playing" | "paused";

export interface QueueState {
  status: PlaybackStatus;
  current: QueueItem | null;
  startedAt?: string;
  items: QueueItem[];
  updatedAt: string;
}

export interface HistoryEntry {
  item: QueueItem;
  startedAt: string;
  endedAt: string;
  endReason: "finished" | "skipped" | "error";
  endedById?: string;
  error?: string;
}

export interface PlayerStatus {
  online: boolean;
  name?: string;
  version?: string;
  connectedAt?: string;
  lastSeenAt?: string;
  itemId?: string;
  positionSeconds: number;
  durationSeconds: number;
  lastError?: string;
}

export interface LiveEvent {
  type: "queue" | "progress" | "player";
  queue?: QueueState;
  player?: PlayerStatus;
}

export interface AuthMethods {
  google: boolean;
  dev: boolean;
}
