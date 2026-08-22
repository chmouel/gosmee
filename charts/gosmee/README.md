# gosmee server Helm chart

Deploys the [gosmee](https://github.com/chmouel/gosmee) **server**: the
public-facing relay that receives webhooks and streams them to `gosmee client`
instances running behind a firewall.

The client side is not covered by this chart, run it wherever it needs to
forward to, see [gosmee-client-deployment.yaml](../../misc/gosmee-client-deployment.yaml)
for a plain manifest.

## Install

```shell
helm install gosmee oci://ghcr.io/chmouel/charts/gosmee \
  --set server.publicUrl=https://smee.example.com
```

From a checkout of this repository:

```shell
helm install gosmee ./charts/gosmee --set server.publicUrl=https://smee.example.com
```

Clients then connect to a channel of that server:

```shell
gosmee client https://smee.example.com/aBcdeFghijklmn http://localhost:8080
```

## Exposing the server

The server is only useful when your webhook provider can reach it. Enable either
an Ingress:

```yaml
ingress:
  enabled: true
  className: nginx
  hosts:
    - host: smee.example.com
      paths:
        - path: /
          pathType: Prefix
  tls:
    - secretName: smee-example-com-tls
      hosts:
        - smee.example.com
```

or a Gateway API `HTTPRoute`:

```yaml
httpRoute:
  enabled: true
  parentRefs:
    - name: my-gateway
      namespace: gateway-system
      sectionName: https
  hostnames:
    - smee.example.com
```

`server.publicUrl` is what gosmee advertises to clients on its web interface.
When left empty it is derived from the first ingress host, or from the first
`HTTPRoute` hostname. Set it explicitly when neither describes the address your
provider actually reaches, ie: behind an external load balancer.

Webhook deliveries are streamed over SSE, so make sure your proxy does not
buffer them. With the nginx ingress controller:

```yaml
ingress:
  annotations:
    nginx.ingress.kubernetes.io/proxy-buffering: "off"
    nginx.ingress.kubernetes.io/proxy-read-timeout: "3600"
```

## Securing the server

A channel URL is a bearer token: anyone who knows it can post to it and read its
stream. The chart leaves the choice of hardening to you.

- `server.secret.webhookSignatures` validates GitHub, GitLab, Gitea and
  Bitbucket signatures and rejects everything else.
- `server.secret.replayToken` locks `POST /replay/{channel}` behind a bearer
  token, it is open otherwise.
- `server.allowedIPs` restricts who may post webhooks. Combined with
  `server.trustProxy` when gosmee sits behind an ingress that overwrites
  `X-Forwarded-For`, and *only* then, see
  [SECURITY.md](../../SECURITY.md#trusting-proxy-headers-safely).
- `server.corsOrigin` defaults to `*`, meaning any website a browser visits can
  read the stream of a channel it knows. Set it to your own origin, or `""` for
  same-origin only.
- `server.encryptedChannels` marks channels as protected: only clients holding
  one of the listed keys can subscribe, and payloads are encrypted end to end.

Secrets are passed as environment variables from a `Secret`, never as command
line arguments, so they do not show up in the pod spec.

```yaml
server:
  secret:
    webhookSignatures:
      - my-github-webhook-secret
    replayToken: my-replay-token
```

To manage that `Secret` yourself, set `server.secret.existingSecret`. It is
loaded with `envFrom`, so its keys must be `GOSMEE_WEBHOOK_SIGNATURE` (comma
separated for several), `GOSMEE_REPLAY_TOKEN` and `GOSMEE_REDIS_URL`.

### Protected channels

Generate a keypair per client with `gosmee keygen --key-file client.json`, it
prints the public key, then:

```yaml
server:
  encryptedChannels:
    enabled: true
    channels:
      my-protected-channel:
        allowed_public_keys:
          - <public key printed by gosmee keygen>
```

The client passes its side with `--encryption-key-file client.json`.

## Running more than one replica

A single replica keeps its channels in memory, so a webhook only reaches the
clients connected to the replica that received it. Set `server.redis.url` to
have deliveries written to Redis Streams and readable from any replica, clients
also resume from where they stopped after a disconnect:

```yaml
replicaCount: 3
podDisruptionBudget:
  enabled: true
server:
  redis:
    url: redis://redis.default.svc.cluster.local:6379/0
    streamMaxlen: 10000
```

The chart refuses to render `replicaCount > 1` without it. Redis itself is not
part of this chart, point it at one you already run.

## Image

A released chart carries the gosmee version it ships with as its `appVersion`,
and that is the image tag it uses, so `--version 0.33.0` gets you the chart and
the binary of that release. Installing from a checkout of the repository follows
`ghcr.io/chmouel/gosmee:main` instead.

Override either half when you need to:

```yaml
image:
  tag: "0.33.0"
  # or resolve to the exact same bytes every time
  digest: sha256:...
```

## Values

| Key | Default | Description |
| --- | --- | --- |
| `replicaCount` | `1` | Number of server replicas, above 1 requires `server.redis.url`. |
| `image.repository` | `ghcr.io/chmouel/gosmee` | Image repository. |
| `image.tag` | `""` | Image tag, defaults to the chart `appVersion`. |
| `image.digest` | `""` | Image digest, takes precedence over the tag. |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy. |
| `imagePullSecrets` | `[]` | Pull secrets for the image. |
| `nameOverride` / `fullnameOverride` | `""` | Override the generated names. |
| `server.publicUrl` | `""` | URL advertised to clients, derived from the ingress or HTTPRoute when empty. |
| `server.port` | `3333` | Port gosmee listens on in the container. |
| `server.address` | `0.0.0.0` | Address gosmee binds to. |
| `server.logLevel` | `info` | `debug`, `info`, `warn` or `error`. |
| `server.logFormat` | `json` | `json` or `pretty`. |
| `server.maxBodySize` | `26214400` | Maximum webhook body size in bytes. |
| `server.corsOrigin` | `"*"` | CORS origin for the SSE endpoint, `""` for same-origin only. |
| `server.trustProxy` | `false` | Trust `X-Forwarded-For` and `X-Real-IP`. |
| `server.allowedIPs` | `[]` | CIDRs or IPs allowed to post webhooks. |
| `server.footer` | `""` | HTML snippet shown in the page footer. |
| `server.secret.webhookSignatures` | `[]` | Tokens validating incoming webhook signatures. |
| `server.secret.replayToken` | `""` | Bearer token required by `POST /replay/{channel}`. |
| `server.secret.existingSecret` | `""` | Secret to use instead of the chart one, keys `GOSMEE_WEBHOOK_SIGNATURE`, `GOSMEE_REPLAY_TOKEN`, `GOSMEE_REDIS_URL`. |
| `server.redis.url` | `""` | Redis URL enabling cross-replica delivery. |
| `server.redis.streamMaxlen` | `10000` | Entries retained per channel stream, 0 disables trimming. |
| `server.encryptedChannels.enabled` | `false` | Enable protected channels. |
| `server.encryptedChannels.channels` | `{}` | Channel id to `allowed_public_keys` mapping. |
| `server.encryptedChannels.existingSecret` | `""` | Secret holding the channels JSON instead. |
| `server.encryptedChannels.key` | `encrypted-channels.json` | Key of the JSON document in that secret. |
| `extraArgs` | `[]` | Extra flags for `gosmee server`, ie: `--auto-cert`. |
| `extraEnv` / `extraEnvFrom` | `[]` | Extra environment for the container. |
| `service.type` | `ClusterIP` | Service type. |
| `service.port` | `80` | Service port. |
| `service.annotations` | `{}` | Service annotations. |
| `service.nodePort` | `null` | Node port when `service.type` is `NodePort`. |
| `ingress.enabled` | `false` | Create an Ingress. |
| `ingress.className` | `""` | Ingress class. |
| `ingress.annotations` | `{}` | Ingress annotations. |
| `ingress.hosts` | see values.yaml | Hosts and paths. |
| `ingress.tls` | `[]` | TLS configuration. |
| `httpRoute.enabled` | `false` | Create a Gateway API HTTPRoute. |
| `httpRoute.parentRefs` | `[]` | Gateways the route attaches to, required when enabled. |
| `httpRoute.hostnames` | `[]` | Hostnames served by the route. |
| `httpRoute.rules` | `[]` | Rules, defaults to sending everything to the gosmee service. |
| `httpRoute.annotations` | `{}` | HTTPRoute annotations. |
| `serviceAccount.create` | `true` | Create a service account. |
| `serviceAccount.name` | `""` | Service account name. |
| `serviceAccount.annotations` | `{}` | Service account annotations. |
| `serviceAccount.automountServiceAccountToken` | `false` | Mount the API token in the pod. |
| `podAnnotations` / `podLabels` | `{}` | Extra pod metadata. |
| `podSecurityContext` | non-root, `RuntimeDefault` seccomp | Pod security context. |
| `securityContext` | no privilege escalation, read-only root, all capabilities dropped | Container security context. |
| `resources` | 100m/512Mi requests, 200m/1Gi limits | Container resources. |
| `livenessProbe` / `readinessProbe` | `GET /health` | Probes, set to `{}` to disable. |
| `terminationGracePeriodSeconds` | `60` | Grace period, deliveries are long-lived SSE connections. |
| `podDisruptionBudget.enabled` | `false` | Create a PodDisruptionBudget. |
| `podDisruptionBudget.minAvailable` | `1` | Minimum available pods. |
| `podDisruptionBudget.maxUnavailable` | `null` | Maximum unavailable pods. |
| `extraVolumes` / `extraVolumeMounts` | `[]` | Extra volumes, ie: TLS certificates used with `extraArgs`. |
| `nodeSelector`, `tolerations`, `affinity`, `topologySpreadConstraints`, `priorityClassName` | empty | Scheduling. |
| `tests.enabled` | `true` | Render the `helm test` pod. |
| `tests.image` | `busybox:1.37` | Image used by the test pod. |

## Notes

The container runs with a read-only root filesystem, an `emptyDir` is mounted on
`/tmp` for the Let's Encrypt cache used by `--auto-cert` and for temporary
files.
