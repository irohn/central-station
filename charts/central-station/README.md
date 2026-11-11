# Central Station Helm chart

The chart runs API, web, Piper TTS and persistent PostgreSQL. All Services are
ClusterIP, with no host ports or host networking. Tailscale Ingress publishes
HTTPS with Funnel disabled. NetworkPolicies allow ingress through Tailscale
proxies in the `tailscale` namespace and restrict component-to-component traffic.
The cluster must enforce NetworkPolicy and have the Tailscale operator installed.

People are signed in one of two ways, `auth.mode`: from the tailnet identity
the Tailscale proxy attaches to every request (`proxy`), or with Google
(`google`, the default). Dev and password login are disabled either way, secure
cookies are enabled, and the default allow-list denies everyone until set.
Player tokens remain available for speaker devices, which run outside the cluster.

Create an existing Secret with `DATABASE_URL`, `POSTGRES_PASSWORD`,
`SESSION_SECRET`, `PLAYER_TOKEN`, and in google mode `GOOGLE_CLIENT_ID` and
`GOOGLE_CLIENT_SECRET` (proxy mode does not read them).
For release `central-station`, the database URL is
`postgres://central:<password>@central-station-central-postgres:5432/central?sslmode=disable`.
Do not put credentials in values files. Changing the database password after
initialization also requires rotating the PostgreSQL role password. Restart API
pods after rotating session/OAuth/player credentials; environment variables are
read at startup. PVCs survive StatefulSet removal; back up PostgreSQL separately.

```sh
helm repo add central-station https://raw.githubusercontent.com/irohn/central-station/master/docs/helm
helm repo update
helm upgrade --install central-station central-station/central-station \
  --namespace skyforce --create-namespace \
  --set publicURL=https://central.tailcfde81.ts.net \
  --set 'auth.allowedDomains[0]=skyforceai.com'
```

Images are pinned to appVersion (0.0.19), with an optional image.tag override.
The nginx config is mounted from Helm so its upstream follows the release name.
Pin workloads to the storage node when using local-path storage.

To publish a new chart, bump Chart.yaml version, then run:

```sh
helm lint charts/central-station
helm package charts/central-station --destination docs/helm
helm repo index docs/helm --url https://raw.githubusercontent.com/irohn/central-station/master/docs/helm
```

Commit the chart, package and index together.

### Sign-in from the tailnet (`auth.mode=proxy`)

```
browser ──https──> Tailscale proxy ──> web (nginx) ──> api
                   sets Tailscale-User-Login            │ peer trusted? allowed? role?
                   (replaces any copy the client sent)  └─ the request runs as that person
```

No login page, no session: the proxy names the person on every request. The
header is a sign-in only because nothing but the proxy can send it, which the
chart arranges in three parts, and refuses to render without:

1. **The proxy overwrites the header** (`tailscale.enabled`): the operator's
   proxy sets `Tailscale-User-Login` and `Tailscale-User-Name` from the tailnet
   identity after removing whatever the client sent. A tagged node gets none.
2. **Only the proxy reaches the web pod, and only the web pod reaches the api**
   (`networkPolicy.enabled`): the NetworkPolicies select the operator's proxy
   for this release's Ingress by the labels the operator puts on it. The
   player pod, when enabled, also reaches the api; it runs this chart's own
   image and uses a zone token, never identity.
3. **The header is believed only from `auth.trustedProxyCIDRs`**: the api's
   peer is the web pod and the web pod's peer is the proxy, both pod addresses
   that change on restart, so the value is the pod range of the node they run
   on minus the node's own addresses. A NetworkPolicy cannot keep out that
   node (kube-router admits node-local traffic ahead of every policy), so
   leaving its addresses out is what refuses it: the api answers 401
   `untrusted_peer` to a request from anywhere else, and nginx in the web pod
   answers 403 `untrusted_peer` on `/api/` for the same peers, so the node
   cannot launder a header through it.

After the peer check the address must pass the allow-list (`auth.allowedDomains`
and friends; `auth.accessAllowlist` cannot be off in this mode). Writes need the
`Origin` to be `publicURL`.

```yaml
auth:
  mode: proxy
  allowedDomains: [example.com]
  adminEmails: [you@example.com]
  trustedProxyCIDRs:        # 10.42.0.0/24 minus .0 and .1 on a k3s first node
    - 10.42.0.2/31
    - 10.42.0.4/30
    - 10.42.0.8/29
    - 10.42.0.16/28
    - 10.42.0.32/27
    - 10.42.0.64/26
    - 10.42.0.128/25
```

After installing, check the refusals rather than the happy path (passing
probes prove nothing: the node's own traffic passes any policy, and a cluster
whose policy controller is off passes everything):

```sh
# From a pod that is not the proxy: no connection to the web pod or the api.
kubectl -n skyforce run np-check --rm -i --restart=Never --image=curlimages/curl -- \
  sh -c 'for u in http://central-station-central-web:3000/ http://central-station-central-api:8080/healthz; do curl -sS -m5 -o /dev/null -w "%{http_code}\n" $u; done'
# From the node: both pods answer, but refuse the header as an untrusted peer.
api=$(kubectl -n skyforce get pod -l app=central-station-central-api -o jsonpath='{.items[0].status.podIP}')
web=$(kubectl -n skyforce get pod -l app=central-station-central-web -o jsonpath='{.items[0].status.podIP}')
curl -s -H 'Tailscale-User-Login: np-check@example.invalid' "http://$api:8080/api/v1/auth/whoami"
curl -s -H 'Tailscale-User-Login: np-check@example.invalid' "http://$web:3000/api/v1/auth/whoami"
# From your own tailnet machine: you.
curl -s https://central.tailcfde81.ts.net/api/v1/auth/whoami
```

The first two must time out, the next two must say `untrusted_peer`, and the
last must name you. `email_not_allowed` from the node would mean the header was
believed: `auth.trustedProxyCIDRs` holds the node's address and must be fixed
before anything else.

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
