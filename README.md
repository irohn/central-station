# Central Station

Shared office music queue. Anyone signs in with Google, collaborators add songs by
pasting a link or typing a search, managers run the queue, admins run the app.

Three containers:

| Service | What it is | Port |
|---|---|---|
| `api` | Go 1.26 API: Google SSO, RBAC, providers (YouTube), queue, WebSocket hub | 8080 (optional) |
| `web` | React web app served by nginx, which also proxies `/api` to the API | 3000 |
| `player` | Headless speaker: Go agent driving mpv + yt-dlp, audio out to the host | none (internal) |
| `db` | Postgres 17: users, queue, playback state, history | none (internal) |

The API is the source of truth. Browsers get live updates over a WebSocket; the player
holds its own WebSocket to the API and plays whatever the queue says.

## Quick start (WSL / Docker)

No local Go or Node toolchain is required; `scripts/go.sh` and `scripts/npm.sh` run
them inside Docker when they are not on your PATH, and the `make` targets use those.

```bash
cp .env.example .env
# edit .env: AUTH_DEV_MODE=true, SESSION_SECRET=<random>, PLAYER_TOKEN=<random>
make test                     # Go unit tests (in-memory stores)
make test-db                  # same plus Postgres integration tests (throwaway container)
make up                       # build + start db, api, web and player
```

Open http://localhost:3000, sign in with the developer login, paste a YouTube link.
Audio comes out of the machine running the `player` container (on WSL2 that is your
Windows audio output through WSLg).

```bash
make logs                     # api logs
docker compose logs -f player # what mpv is doing
make down
```

Without Google credentials the API starts only if `AUTH_DEV_MODE=true`, which mounts a
local-only login endpoint (refused when `APP_ENV=production`):

```bash
curl -s -X POST localhost:8080/api/v1/auth/dev/login \
  -H 'Content-Type: application/json' \
  -d '{"email":"you@example.com","role":"manager"}'
```

The response contains a `token` (use as `Authorization: Bearer <token>`) and also sets
the `cs_session` cookie for browser use.

```bash
TOKEN=...
# Paste a URL
curl -s -X POST localhost:8080/api/v1/queue -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"input":"https://www.youtube.com/watch?v=6TWJaFD6R2s"}'

# Or search (first result wins). "provider" is optional and defaults to youtube.
curl -s -X POST localhost:8080/api/v1/queue -H "Authorization: Bearer $TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"input":"daft punk around the world","provider":"youtube"}'
```

## Google SSO setup

1. Google Cloud Console → APIs & Services → Credentials → *Create credentials* →
   *OAuth client ID* → *Web application*.
2. Authorised redirect URI: `http://localhost:3000/api/v1/auth/google/callback`
   (in general `${BASE_URL}/api/v1/auth/google/callback`; with compose the API is
   reached through the web container, so `BASE_URL` is the web origin).
3. Put the client ID/secret in `.env` as `GOOGLE_CLIENT_ID` / `GOOGLE_CLIENT_SECRET`.
4. Optional: `ALLOWED_EMAIL_DOMAINS=yourcompany.com` restricts sign-in to your Workspace;
   `ADMIN_EMAILS=you@yourcompany.com` bootstraps the first admin.

Browser flow: send the user to `GET /api/v1/auth/google/login?redirect=/queue`. After
Google returns, the API sets an `HttpOnly` session cookie and redirects to
`${FRONTEND_URL}${redirect}`. Failures redirect with `?error=<reason>`.

Sessions are HS256-signed JWTs (`SESSION_SECRET`) containing only the user ID; the role
is looked up per request, so role changes take effect immediately.

## Roles

Roles are cumulative. Each includes everything below it.

| Role | Adds |
|---|---|
| `listener` | `queue:read`, `history:read`, `track:resolve` |
| `collaborator` | `queue:add`, `queue:remove_own`, `playlist:write` |
| `manager` | `queue:remove_any`, `queue:reorder`, `playback:control` (skip / pause / resume / override), `output:control` (pick the speakers) |
| `admin` | `settings:manage`, `users:manage`, `integrations:manage` (speakers, Spotify/Apple Music links) |

New users get `DEFAULT_ROLE` (default `listener`). Emails in `ADMIN_EMAILS` are always
admins. Admins change roles with `PATCH /api/v1/users/{id}`.

## API

All endpoints are under `/api/v1` and return JSON. Errors have one shape:

```json
{"error":{"code":"unsupported_provider","message":"Unsupported provider: https://vimeo.com","details":{"baseUrl":"https://vimeo.com"}}}
```

| Method | Path | Permission | Notes |
|---|---|---|---|
| GET | `/healthz`, `/readyz` | – | liveness / readiness |
| GET | `/api` | – | lists API versions |
| GET | `/api/v1/auth/methods` | – | which sign-in methods are enabled (`google`, `password`, `demo`, `dev`, `passwordVerification`) |
| GET | `/api/v1/auth/google/login` | – | starts Google sign-in (`?redirect=` optional) |
| GET | `/api/v1/auth/google/callback` | – | OAuth callback |
| POST | `/api/v1/auth/dev/login` | – | dev mode only |
| POST | `/api/v1/auth/logout` | – | clears the cookie |
| GET | `/api/v1/me` | signed in | current user + permissions |
| GET | `/api/v1/roles` | signed in | role → permission matrix |
| GET | `/api/v1/providers` | signed in | registered providers, default marked |
| POST | `/api/v1/tracks/resolve` | `track:resolve` | resolve URL / search without queueing |
| GET | `/api/v1/queue` | `queue:read` | status, current item, queued items |
| POST | `/api/v1/queue` | `queue:add` | body `{"input": "...", "provider": "youtube"}`; `"playNext": true` (`queue:reorder`) or `"playNow": true` (`playback:control`) jump the queue |
| DELETE | `/api/v1/queue/{id}` | `queue:remove_own` / `queue:remove_any` | collaborators only their own |
| PATCH | `/api/v1/queue/{id}` | `queue:reorder` | body `{"position": 0}` |
| GET | `/api/v1/history?limit=50` | `history:read` | newest first |
| GET | `/api/v1/playback` | `queue:read` | status summary |
| POST | `/api/v1/playback/skip` | `playback:control` | ends current, promotes next |
| POST | `/api/v1/playback/pause`, `/resume` | `playback:control` | |
| POST | `/api/v1/playback/complete` | `playback:control` | manual "track ended" (the player normally reports this over its socket) |
| GET | `/api/v1/player` | signed in | player status: online, name, position |
| GET | `/api/v1/player/ws` | zone player token | player WebSocket (see below) |
| GET | `/api/v1/zones` | signed in | zones with player + playback summary |
| POST | `/api/v1/zones` | `integrations:manage` | create a zone; response carries its player token once |
| PATCH / DELETE | `/api/v1/zones/{zone}` | `integrations:manage` | rename / delete |
| POST | `/api/v1/zones/{zone}/token` | `integrations:manage` | issue a new player token |
| any | `/api/v1/zones/{zone}/queue`, `/history`, `/playback/*`, `/settings/fallback*`, `/broadcasts`, `/player`, `/events/ws` | as above | zone-scoped forms of the routes above |
| GET | `/api/v1/events/ws` | signed in | live queue/player events for browsers |
| GET | `/api/v1/users` | signed in | |
| PATCH | `/api/v1/users/{id}` | signed in / `users:manage` | `{"name": "..."}` (own name for anyone), `{"role": "..."}` / `{"emailVerified": true}` (admins) |
| PUT / DELETE | `/api/v1/users/{id}/permissions` | `users:manage` | replace / reset a user's permissions for the running API instance (not persisted) |

### Input resolution

`input` is classified once:

- Looks like a URL (`https://…`, or `youtu.be/…` style host+path without scheme) →
  the host picks the provider. Unknown host → `400 unsupported_provider` with
  `"Unsupported provider: <scheme>://<host>"`. A YouTube URL that is not a video
  (channel, playlist) → `400 invalid_url`.
- Otherwise it is a search string. The provider is `provider` from the JSON body if
  given, else `DEFAULT_PROVIDER`. Unknown provider id → `400 unknown_provider`.
  The first result is returned; no results → `404 track_not_found`.

The override lives in the JSON body rather than a header because it is part of the
request's meaning, not transport metadata: it is visible in logs and client code, is
validated with the rest of the body, and does not need CORS header allow-listing.

### Zones (several players, several rooms or sites)

A zone is one player node with its own queue, playback state, history, default
playlist, volume and announcement queue. The web app has a zone switcher in the
top bar; everything on the page (queue, now playing, history, default playlist,
broadcasts) is the selected zone's. Admins manage zones from the gear menu.

- Every deployment has a default zone (`Office`). Pre-zone data was migrated
  into it, and the legacy `PLAYER_TOKEN` environment variable still
  authenticates a player for it, so an existing single-player setup keeps
  working with no changes.
- Creating a zone issues a **player token**, shown once. Run a player container
  next to that zone's speakers with `PLAYER_TOKEN=<that token>` and
  `API_URL=<where the API is reachable from that machine>`. The player dials
  out, so it can sit on any network that can reach the API URL; nothing is
  opened inbound. "Issue a new token" on the Zones page rotates it and drops the
  currently connected player until it has the new one.
- Zone-scoped routes live under `/api/v1/zones/{zone}/...` (queue, history,
  playback, settings/fallback, broadcasts, player, events/ws). The same paths
  without the prefix address the default zone. `GET /api/v1/zones` lists every
  zone with its player and playback summary; `POST /api/v1/zones`,
  `PATCH|DELETE /api/v1/zones/{zone}` and `POST /api/v1/zones/{zone}/token`
  need `integrations:manage` (admins). The last zone cannot be deleted.
- Announcements go to the zone you are viewing, or to every zone
  (`"allZones": true`, "Send to: All zones" in the UI). Alerts carry a
  `zoneId` (empty = every zone).
- Browsers subscribe to one zone's event stream; a `zones` event with every
  zone's summary rides on it so the switcher shows live status for all zones.

### Player protocol

`GET /api/v1/player/ws` (authenticated with `Authorization: Bearer <zone player token>`,
or the legacy `PLAYER_TOKEN` for the default zone) is the
player socket; `GET /api/v1/events/ws` (session cookie) streams read-only events to
browsers. Both are JSON, one object per message.

- API to player: `{"type":"state","state":{"status":"playing","current":{...}}}`.
  The API always sends the full desired state. The player reconciles it against mpv
  (load, pause, stop), so reconnects and missed messages are harmless.
- Player to API: `hello` (name, version), `progress` (item id, position, duration,
  every 2 seconds, also used as a heartbeat), `ended` with reason `eof` or `error`.
  On `ended` the API records history and advances the queue, which triggers a new
  `state` push. An `ended` for an item that is no longer current is ignored.
- Browser events: `queue` (full snapshot plus player status), `progress`, `player`
  (online, offline, name).

A second player connection replaces the first. `GET /api/v1/player` returns the
current status for the UI.

### Releases and images

`.github/workflows/release.yml` runs on every push to `master`: it tests, builds
`central-station-api`, `-web` and `-player` for `linux/amd64` and `linux/arm64`,
pushes them to GitHub Container Registry, and only then tags the commit.

- Versions are semver git tags (`v0.0.1`, `v0.0.2`, ...). Each push bumps the
  patch version; put `[minor]` or `[major]` in the commit message, or run the
  workflow manually and pick the bump, to bump the other parts.
- Images: `ghcr.io/<owner>/central-station-<name>:<version>`, also tagged
  `<major>.<minor>`, `<major>` and `latest`. The player reports the version in its
  hello message and the API logs it on startup.
- Pulling from a private package needs a classic personal access token with
  `read:packages` (`docker login ghcr.io -u <user>`; in Kubernetes a
  `docker-registry` pull secret).

### Broadcasts (announcements)

Managers (`broadcast:send`) can interrupt the music with an announcement from
the gear menu → Broadcast: record one in the browser (up to 60 s), type one
(spoken by the `tts` service, English voices), or reuse a saved clip, then
choose how many times it repeats (1–10) and which chime precedes it. The player
downloads the audio, ducks the music to 15%, plays the chime and the clip in a
second mpv process, and restores the volume; announcements sent meanwhile wait
in line. Everyone sees a banner while one plays.

Clips (recordings, TTS output, chimes) live in the `clips` table. Three chimes
are bundled; admins can upload more and pick the default (`PUT /settings/chime`).
API: `GET/POST /clips`, `POST /clips/tts`, `GET /clips/{id}/audio`,
`PATCH|DELETE /clips/{id}`, `POST /broadcasts {"clipId","loops","chimeId"}`,
`GET /broadcasts`.

The `tts` image (`tts/`) wraps [Piper](https://github.com/OHF-Voice/piper1-gpl)
with fourteen bundled voices: twelve US English (Norman, Amy, Hannah, Henry,
Joe, Kristin, Mike, Ryan, Leslie, Libby, Liberty, Lina) and two British (Jenny,
Alan). The default is Norman (`en_US-norman-medium`). The friendly names and
the speaker picked from Piper's multi-speaker models live in `tts/server.py`;
the id list is the `PIPER_VOICES` build arg in `tts/Dockerfile` (samples:
https://rhasspy.github.io/piper-samples/). `GET /voices` on the service lists
them; admins choose the default (`PUT /settings/tts`) and every typed
announcement can pick one. Models load on first use and only the two most
recently used stay in memory; the "high" models need roughly twice the memory
of "medium" ones, so give the service about 1.5 GB when two can be loaded. Set `TTS_URL` on the API to enable typed
announcements; `POST /clips/tts/preview` synthesises without saving.

Storage is bounded: at most `CLIPS_MAX_SAVED` user clips (default 50, bundled
chimes excluded), each at most `CLIPS_MAX_BYTES` (5 MB) and
`CLIPS_MAX_DURATION_SECONDS` (60), typed text at most `TTS_MAX_CHARS` (600).
Past the count the API refuses new clips (`409 clip_limit`) rather than deleting
old ones, so a clip an alert depends on can never vanish silently. Because two mpv processes share the sound card, the player must
use a mixing ALSA device (`MPV_AUDIO_DEVICE=alsa/default:CARD=<name>`, i.e. dmix)
rather than `plughw`, or PulseAudio.

### Alerts (scheduled announcements)

Managers (`alerts:manage`) schedule clips from the gear menu → Alerts: a name,
a time and weekdays (or a raw 5-field cron expression), a timezone (default
`ALERTS_TIMEZONE`), a saved clip, a repeat count and a chime. A scheduler in the
API checks every 30 seconds and fires due alerts through the broadcast path.
Occurrences missed by more than five minutes (for example while the API was
down) are skipped, never replayed. Each alert has a `type`; only `cron` exists
today, so event- or webhook-driven alerts can be added later without changing
the shape. API: `GET/POST /alerts`, `PATCH|DELETE /alerts/{id}`,
`POST /alerts/{id}/test`.

### Queued playlists

Collaborators can paste a whole playlist (`youtube.com/playlist?list=…`,
`music.youtube.com/playlist?list=…` or `music.youtube.com/browse/VL…`) into the
same Add box. Playlists form a second queue with lower priority than songs: the
song queue plays first, then each queued playlist in the order added, then the
default playlist loops. A playlist is shuffled by default; its owner (or a
manager, `playlist:remove_any`) can toggle shuffle or skip the whole playlist
from the Playlists card. Tracks it produces carry `source: "playlist"` plus the
playlist id and title. API: `POST /queue {"input": <playlist url>, "shuffle": bool}`,
`PATCH /queue/playlists/{id} {"shuffle": bool}`, `DELETE /queue/playlists/{id}`.

### Reaching the app under several names

Browsers must use an origin the API knows: `FRONTEND_URL`, or any entry in
`ALLOWED_ORIGINS` (comma-separated `scheme://host[:port]`). Those origins are
allowed for CORS, for cookie-authenticated writes and for the live WebSocket;
anything else loads the page but never receives live updates. Set it when the
same deployment is reached via a LAN or Tailscale name as well.
`ALLOWED_ORIGINS=*` accepts every origin (demo mode implies this); cross-site
protection then rests on the `SameSite=Lax` session cookie alone.

### Email + password sign-in

`AUTH_PASSWORD_ENABLED=true` adds register/sign-in forms next to Google. Passwords
are hashed with argon2id (64 MiB, 3 passes) and never stored or logged in clear;
sign-in is rate-limited (10 failures per email and address per 15 minutes) and
answers identically for unknown, Google-only and wrong-password attempts. New
accounts start as `AUTH_UNVERIFIED_ROLE` (listener) and are promoted to
`AUTH_VERIFIED_ROLE` (collaborator) once the address is verified, either by the
link emailed through SMTP (`SMTP_*`) or, without SMTP, by an admin pressing
Verify on the Users page. `ADMIN_EMAILS` only grant admin to verified addresses,
so nobody can claim an admin email without proving it. The allow-list applies to
registration too. Users can change their password from the gear menu.

### Demo mode (self-service accounts)

`AUTH_DEMO_MODE=true` turns the deployment into a walk-up demo: anyone who opens
the app registers with an email and password and can add songs straight away.
Concretely it

- switches password sign-in on (`AUTH_PASSWORD_ENABLED` is implied) and Google
  SSO off, even when Google credentials are configured (the API logs a warning);
- skips the verification step: new accounts are marked verified at once and
  start as `AUTH_VERIFIED_ROLE` (collaborator by default). No verification
  email is sent, the verify endpoints are not mounted, and accounts created
  before the switch are promoted the next time they sign in;
- requires `ADMIN_EMAILS`. Those addresses become admins the moment they
  register and change everyone else's role or permissions from the Users page.
  Because nothing is verified, whoever registers an admin address first owns
  it, so register before sharing the link;
- keeps the sign-in allow-list: with `ALLOWED_EMAIL_DOMAINS=domain.com` only
  `@domain.com` addresses (and the admins) can register, and
  `ACCESS_ALLOWLIST=false` opens it to everyone while keeping the list on file;
- accepts browsers on any origin (as if `ALLOWED_ORIGINS=*`), so people can
  open the demo by whatever LAN name or address they have without listing
  each one.

```bash
AUTH_DEMO_MODE=true
ADMIN_EMAILS=you@domain.com
ALLOWED_EMAIL_DOMAINS=domain.com   # optional
```

Demo mode is meant for showing the app around, not for the public internet: any
email can be claimed without proof. Turn it off (and set up Google or SMTP) once
the deployment is real.

### Sign-in allow-list

`ALLOWED_EMAIL_DOMAINS` and `ALLOWED_EMAILS` list who may sign in; `ADMIN_EMAILS`
are always allowed. `ACCESS_ALLOWLIST=true|false` switches enforcement on or off
without touching the lists (unset: enforced iff a list is non-empty). Keep the
lists filled and flip the flag when the app's exposure changes, e.g. from a
private tailnet to the public internet.

### Roles and permissions

Roles are cumulative (`listener < collaborator < manager < admin`) and each level
introduces a set of permissions (see `internal/rbac`). The sets can be replaced
per level with `ROLE_PERMISSIONS_<ROLE>` (comma-separated permission names), so a
deployment can, say, let collaborators skip tracks without a code change. Unknown
names fail startup; admins always hold every permission. `GET /api/v1/roles`
shows the active policy.

### Volume

Collaborators and above set the output level from the Now Playing card (`POST
/api/v1/playback/volume {"volume": 0-100}`, permission `volume:control`). It is mpv's software volume, applied
by the player on every state push and persisted in the `settings` table, so 100%
equals whatever the host mixer is set to. Everyone sees the current level in the
player status.

### Default playlist

`POST /settings/fallback/reset` restores the playlist configured on the server
(`FALLBACK_PLAYLIST_URL`); the response of `GET /settings/fallback` carries it
as `envDefault` so the UI can offer "Reset to default".

When the queue is empty the player falls back to a configurable YouTube playlist
so the office never goes silent. Tracks it plays are marked `source: "fallback"`
in the queue state and history, and are never shown inside the users' queue; the
UI shows the playlist and its next track in a separate card.

- `FALLBACK_PLAYLIST_URL` / `FALLBACK_SHUFFLE` seed the setting the first time the
  server starts with nothing configured. After that the value lives in the
  `settings` table and is edited from the UI (gear menu → Default playlist) by
  managers and admins (`fallback:manage`).
- A user-queued track never interrupts the current fallback track; it plays
  right after it. Changing or disabling the playlist skips a currently playing
  fallback track.
- The playlist is re-fetched in the background every 6 hours (and on demand with
  the Refresh button) so newly added videos show up. With `YOUTUBE_API_KEY` set up
  to 300 tracks are used; without a key the public playlist page is parsed, which
  only exposes the first 100.
- API: `GET /api/v1/settings/fallback`, `PUT /api/v1/settings/fallback`
  `{"url","enabled","shuffle"}`, `POST /api/v1/settings/fallback/refresh`.

### Choosing the output device (USB, Bluetooth, HDMI…)

Each zone's player reports the outputs its node can see, and managers pick one
from the **Now playing** card (permission `output:control`, manager by default).
"System default" follows the device the node was configured with
(`MPV_AUDIO_DEVICE`, e.g. the Pi's USB speaker in ALSA mode) or, when none is
set, whatever the node's OS uses as its default output; picking a device pins
the zone to it. Switching happens mid-track with a gap of
about a tenth of a second; volume and announcement ducking carry over.

- The list comes from mpv (`audio-device-list`), so it is the same code on every
  platform. Linux nodes with PipeWire or PulseAudio list every sink, Bluetooth
  speakers included as soon as the OS connects them. In ALSA mode
  (`MPV_AO=alsa`) only sound cards appear and Bluetooth never does. On macOS,
  PulseAudio on the Mac publishes each CoreAudio device as a sink, so the same
  picker works there.
- Pairing and connecting Bluetooth stays with the operating system; the picker
  switches between devices the OS already has.
- The choice is stored per zone and re-applied when the player reconnects. If
  the chosen device is unplugged the player falls back to the default output and
  the UI shows "not found, using default"; it switches back automatically when
  the device reappears.
- API: `POST /api/v1/zones/{zone}/playback/output` with
  `{"device": "auto" | "<id from audioDevices>"}`; `GET .../player` carries
  `audioDevices`, `audioDevice` (requested), `activeAudioDevice` and
  `audioDeviceMissing`.

### When the speakers go away (faults)

The zone is paused rather than skipped through when the player cannot play:

- **No audio output** (device unplugged, sound server gone): the player reports
  it, the API pauses the zone on the current track, shows "No audio output" in
  Now playing, and nothing is written to history. The player keeps retrying the
  track every few seconds; as soon as mpv can open an output again the zone
  resumes by itself. A player reconnect (node reboot) also resumes.
- **Tracks keep failing** (three in a row, e.g. yt-dlp out of date): the zone
  pauses on the next track with a "Playback keeps failing" notice instead of
  burning through the queue. Fix the cause (usually rebuild the player image)
  and press Resume; a track that plays for a few seconds clears the fault.

Both faults appear in `GET .../player` as `fault: {kind, message, since}` and in
the zone switcher.

### Player container and audio

`Dockerfile.player` builds a Debian image with mpv, the latest yt-dlp release, and the
Go agent from `cmd/player`. It runs unprivileged and publishes no ports.

Audio leaves the container through a host audio socket:

| Host | `.env` settings |
|---|---|
| WSL2 (WSLg) | `PULSE_SOCKET=/mnt/wslg/PulseServer`, `MPV_AO=pulse` (defaults) |
| Linux desktop (PulseAudio or PipeWire) | `PULSE_SOCKET=/run/user/1000/pulse/native`, `MPV_AO=pulse`. If the server rejects the connection, also mount `~/.config/pulse/cookie` to `/tmp/.config/pulse/cookie` |
| Headless Linux server, no audio daemon | remove the pulse volume, uncomment `devices: /dev/snd` and `group_add: audio` in the compose file, set `MPV_AO=alsa` (optionally `MPV_AUDIO_DEVICE=alsa/hw:0,0`) |
| macOS (Docker Desktop) | No socket mount is possible; the player connects over TCP to PulseAudio running on the Mac. `brew install pulseaudio`, run `./scripts/mac-audio.sh` (listens on `127.0.0.1:4713`, plays through the Mac's current output device), and set `COMPOSE_FILE=docker-compose.yml:docker-compose.mac.yml` in `.env` so `make up` applies the override. Restart the script after a reboot. |

yt-dlp is fetched at image build time. When YouTube changes its pages and tracks start
failing with "error" in the history, rebuild the player image:

```bash
docker compose build --no-cache player && docker compose up -d player
```

Note: yt-dlp/mpv extract the audio stream outside YouTube's official player, which is
against YouTube's terms of service. This is a proof of concept; a terms-compliant
player (a kiosk browser running the official embed) can be swapped in behind the same
protocol later.

### Providers

`internal/provider.Provider` is the interface; `internal/provider/youtube` is the first
implementation. YouTube works without credentials (results-page scrape for the first
video ID + oEmbed for metadata). Set `YOUTUBE_API_KEY` to use the Data API v3 instead,
which is more robust and adds durations. Adding Spotify or Apple Music means implementing
the interface and registering it in `cmd/api/main.go`.

### Versioning

- Major version in the path (`/api/v1`) and in the Go package (`internal/api/v1`).
  DTOs live in that package and are never shared with a later version.
- Within a version only additive changes: new optional fields, new endpoints.
  Removing or renaming anything means `internal/api/v2` mounted beside v1.
- Every response carries `API-Version: 1`; `GET /api` lists versions and their status so
  a retiring version can be flagged `deprecated` (with `Deprecation`/`Sunset` headers)
  before it is removed.
- Domain packages (`queue`, `provider`, `rbac`, `store`) are unversioned; only the HTTP
  shape is.

## Web app

`web/` is a small React + TypeScript app built with Vite. Tabs: Queue (now playing,
add form, up-next list with the actions your role allows), History, and Users (admins
change roles). It keeps state live through the events WebSocket and falls back to
reconnecting with backoff.

Local development without Docker for the frontend:

```bash
./scripts/npm.sh install
./scripts/npm.sh run dev        # http://localhost:3000, proxies /api to localhost:8080
```

## Layout

```
cmd/api/              API main: wiring + graceful shutdown
cmd/player/           player main: mpv + agent
internal/api/v1/      versioned HTTP handlers and DTOs
internal/auth/        Google OIDC, session tokens, auth + CSRF + permission middleware
internal/config/      env config
internal/db/          Postgres pool + embedded migrations
internal/httpx/       JSON helpers, typed API errors
internal/player/      player protocol types + WebSocket hub (server side)
internal/playerd/     player agent: mpv IPC client + reconcile loop
internal/provider/    Provider interface + registry; youtube/ implementation
internal/queue/       queue logic + Store interface (memory, Postgres)
internal/rbac/        roles and permissions
internal/server/      root router, health, CORS, logging, recovery
internal/store/       user store interface (memory, Postgres)
scripts/go.sh         runs go locally or in Docker
scripts/npm.sh        runs npm locally or in Docker
web/                  React app + nginx config + Dockerfile
```

### Persistence

`DATABASE_URL` selects Postgres; when it is empty (development only) users and the
queue live in memory and vanish on restart. Production refuses to start without it.

- Schema lives in `internal/db/migrations/*.sql`, embedded in the binary and applied
  at startup in filename order (tracked in `schema_migrations`, guarded by an
  advisory lock). Add a migration by dropping a new `NNNN_name.sql` file there.
- `store.UserStore` has memory and Postgres implementations.
- `queue.Store` is the queue's persistence seam. The `queue.Service` keeps the logic
  and an in-memory mirror, serialises mutations, and writes through to the store
  before touching the mirror. `Transition` bundles "record history, pop next, set
  playback" in one transaction so a crash never leaves a half-advanced queue.
- Both implementations run the same conformance tests; the Postgres ones need
  `TEST_DATABASE_URL` (`make test-db` handles that).

Data is kept in the `pgdata` compose volume. `docker compose down -v` wipes it.

## Configuration

See `.env.example`. Production requires `APP_ENV=production`, a `SESSION_SECRET`,
`DATABASE_URL`, Google credentials and `COOKIE_SECURE=true` behind HTTPS. `AUTH_DEV_MODE` is refused in
production.
