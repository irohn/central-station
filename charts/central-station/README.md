# Central Station Helm chart

The chart runs API, web, Piper TTS and persistent PostgreSQL. All Services are
ClusterIP, with no host ports or host networking. Tailscale Ingress publishes
HTTPS with Funnel disabled. NetworkPolicies allow ingress through Tailscale
proxies in the `tailscale` namespace and restrict component-to-component traffic.
The cluster must enforce NetworkPolicy and have the Tailscale operator installed.

Google is the only human sign-in method; dev and password login are disabled,
secure cookies are enabled, and the default allow-list denies everyone until set.
Player tokens remain available for speaker devices, which run outside the cluster.

Create an existing Secret with `DATABASE_URL`, `POSTGRES_PASSWORD`,
`SESSION_SECRET`, `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET`, and `PLAYER_TOKEN`.
For release `central-station`, the database URL is
`postgres://central:<password>@central-station-central-postgres:5432/central?sslmode=disable`.
Do not put credentials in values files. Changing the database password after
initialization also requires rotating the PostgreSQL role password. Restart API
pods after rotating session/OAuth/player credentials; environment variables are
read at startup. PVCs survive StatefulSet removal; back up PostgreSQL separately.

```sh
helm repo add central-station https://raw.githubusercontent.com/irohn/central-station/codex/helm-tailscale/docs/helm
helm repo update
helm upgrade --install central-station central-station/central-station \
  --namespace skyforce --create-namespace \
  --set publicURL=https://central.tailcfde81.ts.net \
  --set 'auth.allowedDomains[0]=skyforceai.com'
```

Images are pinned to appVersion (0.0.17), with an optional image.tag override.
The nginx config is mounted from Helm so its upstream follows the release name.
Pin workloads to the storage node when using local-path storage.

To publish a new chart, bump Chart.yaml version, then run:

```sh
helm lint charts/central-station
helm package charts/central-station --destination docs/helm
helm repo index docs/helm --url https://raw.githubusercontent.com/irohn/central-station/codex/helm-tailscale/docs/helm
```

Commit the chart, package and index together. When merging, update the repository
URL in this document and GitOps to the intended long-lived branch.

### Optional workstation player

Set `player.enabled=true`, select the speaker's node with `player.nodeSelector`,
and set `player.uid`, `player.gid`, `player.pulseDirectory` and optionally
`player.pulseCookie` to match its desktop user. The player connects to the API
with the existing PLAYER_TOKEN. It mounts only that user's PulseAudio socket
directory and cookie read-only, runs unprivileged and exposes no ports. The
host's default output (including Bluetooth) is used unless a zone selects an
output in the app. PipeWire/PulseAudio must be running on the host; Bluetooth
pairing and reconnection remain host responsibilities. With user lingering
disabled, the desktop audio session may stop after logout.
