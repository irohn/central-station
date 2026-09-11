export type Role = "listener" | "collaborator" | "manager" | "admin";

export type Permission =
  | "queue:read" | "history:read" | "track:resolve"
  | "queue:add" | "queue:remove_own" | "playlist:write" | "volume:control"
  | "queue:remove_any" | "queue:reorder" | "playback:control" | "fallback:manage" | "playlist:remove_any" | "broadcast:send"
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
  /** For fallback items this is the playlist title. */
  addedByName: string;
  addedAt: string;
  /** "playlist" for a user-queued playlist, "fallback" for the default playlist. */
  source?: "fallback" | "playlist";
  playlistId?: string;
  playlistTitle?: string;
}

/** A user-queued playlist (plays after the song queue, before the default). */
export interface QueuedPlaylist {
  id: string;
  provider: string;
  url: string;
  title: string;
  thumbnailUrl?: string;
  trackCount: number;
  played: number;
  shuffle: boolean;
  addedById: string;
  addedByName: string;
  addedAt: string;
  next?: Track;
  playing: boolean;
}

/** Default-playlist summary embedded in every queue snapshot. */
export interface FallbackState {
  enabled: boolean;
  playlistTitle?: string;
  playlistUrl?: string;
  trackCount: number;
  shuffle: boolean;
  next?: Track;
}

export interface FallbackPlaylist {
  provider: string;
  id: string;
  url: string;
  title: string;
  trackCount: number;
  fetchedAt: string;
  tracks?: Track[];
}

/** GET/PUT /settings/fallback */
export interface FallbackView {
  url: string;
  enabled: boolean;
  shuffle: boolean;
  playlist?: FallbackPlaylist;
  next?: Track;
  updatedAt?: string;
}

export type PlaybackStatus = "idle" | "playing" | "paused";

export interface QueueState {
  status: PlaybackStatus;
  current: QueueItem | null;
  startedAt?: string;
  items: QueueItem[];
  updatedAt: string;
  playlists: QueuedPlaylist[];
  fallback?: FallbackState;
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
  /** Output level 0-100 requested from the player. */
  volume: number;
  /** Announcement playing over the music right now. */
  broadcast?: BroadcastStatus;
  broadcastsQueued: number;
}

export interface BroadcastStatus {
  id: string;
  clipId: string;
  clipName: string;
  loops: number;
  byId: string;
  byName: string;
  startedAt?: string;
}

export type ClipKind = "recording" | "tts" | "chime";

export interface Clip {
  id: string;
  name: string;
  kind: ClipKind;
  mime: string;
  size: number;
  durationSeconds?: number;
  text?: string;
  voice?: string;
  createdById?: string;
  createdByName?: string;
  createdAt: string;
  builtin: boolean;
}

export interface Voice {
  id: string;
  label: string;
  language: string;
  region?: string;
  quality?: string;
}

export interface ClipsResponse {
  clips: Clip[];
  defaultChimeId: string;
  ttsAvailable: boolean;
  voices: Voice[];
  defaultVoice: string;
  savedCount: number;
  maxSaved: number;
  maxDurationSec: number;
  maxBytes: number;
  maxLoops: number;
  maxTextChars: number;
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
