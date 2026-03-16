# Security

## Security Model and Trust Boundaries

gosmee is a **relay**, not a firewall. It forwards webhook payloads from a public ingress point to clients running in private networks — it does not inspect, filter, or sanitize payload content beyond the controls described in this document.

```text
internet → [gosmee server] → SSE stream → [gosmee client] → local service
```

**What gosmee can protect:**

- Webhook authenticity (signature validation, IP allowlisting)
- Payload confidentiality on the relay stream (end-to-end encryption)
- Server availability (payload size and channel name limits)

**What gosmee does not handle by itself:**

- TLS for the server — terminate TLS at a reverse proxy (nginx, Caddy, etc.)
- Authentication of clients connecting to the web UI
- Sanitization of payload content delivered to local services

---

## Threat Model

| Threat | Relevant controls |
|---|---|
| Forged or tampered webhooks from untrusted senders | Signature validation, IP allowlisting |
| Eavesdropping on the SSE relay stream | End-to-end encryption |
| Payload-based resource exhaustion (DoS) | `--max-body-size`, channel name length limit |
| Command injection via exec scripts | `--exec` hardening, signature validation, IP allowlisting |
| Unauthorized access to protected channels | Encrypted channels with public-key authentication |

---

## Recommended Baseline

If you do nothing else, apply these controls before deploying gosmee in production, ordered by impact:

- [ ] Run gosmee server behind TLS (nginx, Caddy, or similar)
- [ ] Enable `--webhook-signature` with your provider's shared secret
- [ ] Enable `--allowed-ips` if source IPs are known and stable
- [ ] Set `--max-body-size` to a sensible limit for your workloads
- [ ] Run as a non-root user with minimal filesystem permissions
- [ ] Enable encrypted channels for sensitive payloads
- [ ] If using `--exec`, validate and sanitize all payload fields in scripts before passing them to shell commands

---

## Protecting the Webhook Intake

IP allowlisting and signature validation are complementary controls. Use both where possible: IP restrictions are coarse-grained (network-level, easy to configure) and signatures are fine-grained (cryptographic, provider-verified). An attacker who spoofs a source IP still fails signature validation; an attacker who obtains a signature secret but sends from a blocked IP is still rejected.

### Restricting Webhook Sources by IP

If you know which IP ranges your webhooks will come from, restrict them with `--allowed-ips`. Requests from other IPs receive a 403 and are logged. The restriction applies only to POST requests — the web UI remains open.

```shell
# Accept webhooks from GitHub's ranges only
gosmee server --trust-proxy \
  --allowed-ips 192.30.252.0/22 \
  --allowed-ips 185.199.108.0/22 \
  --allowed-ips 140.82.112.0/20

# GitLab.com
gosmee server --trust-proxy \
  --allowed-ips 35.231.145.151 \
  --allowed-ips 34.74.90.64 \
  --allowed-ips 34.74.226.93

# Bitbucket Cloud
gosmee server --trust-proxy \
  --allowed-ips 34.199.54.113 \
  --allowed-ips 34.232.119.183 \
  --allowed-ips 34.236.25.177 \
  --allowed-ips 35.171.175.212
```

Use `--trust-proxy` when gosmee sits behind a reverse proxy so that `X-Forwarded-For` / `X-Real-IP` headers are used for the client IP. Both IPv4 and IPv6 addresses and CIDR ranges are supported. You can also set allowed IPs via the `GOSMEE_ALLOWED_IPS` environment variable (comma-separated) and enable proxy trust via `GOSMEE_TRUST_PROXY`.

Official IP range docs: [GitHub](https://docs.github.com/en/authentication/keeping-your-account-and-data-secure/about-githubs-ip-addresses) · [GitLab.com](https://docs.gitlab.com/ee/user/gitlab_com/index.html#ipv4-addresses) · [Bitbucket Cloud](https://support.atlassian.com/bitbucket-cloud/docs/what-are-the-bitbucket-cloud-ip-addresses-i-should-use-to-configure-my-corporate-firewall/)

### Validating Webhook Signatures

Signature validation ensures that incoming webhooks are genuinely from your provider and haven't been tampered with. Enable it by passing one or more secrets:

```shell
gosmee server --webhook-signature=SECRET1 --webhook-signature=SECRET2
```

gosmee automatically detects the provider from the request headers and validates accordingly:

| Provider | Header validated |
|---|---|
| GitHub | `X-Hub-Signature-256` (HMAC-SHA256) |
| GitLab | `X-Gitlab-Token` (constant-time comparison) |
| Bitbucket Cloud/Server | `X-Hub-Signature` (HMAC-SHA256) |
| Gitea / Forgejo | `X-Gitea-Signature` (HMAC-SHA256) |

Requests with a missing or invalid signature are rejected with HTTP 401. When multiple secrets are configured, each is tried in turn — useful when migrating secrets or receiving webhooks from multiple sources. The overhead is negligible (~2 μs per request).

Secrets can also be set via `GOSMEE_WEBHOOK_SIGNATURE` (comma-separated).

---

## Protecting the Relay Stream

Once a webhook passes ingress checks (IP allowlisting, signature validation), it travels over the SSE stream to the client. End-to-end encryption protects this leg of the relay from eavesdropping, even if the SSE connection itself is unencrypted.

TLS for the connection is a complement, not a substitute — enable it at your reverse proxy to protect the transport layer.

### How End-to-End Encryption Works

gosmee uses **NaCl `box`** (Curve25519 + XSalsa20-Poly1305). For each SSE message on a protected channel, the server generates a fresh ephemeral Curve25519 keypair and a random 24-byte nonce, then seals the payload with `box.Seal` addressed to the recipient's public key. This gives per-message forward secrecy: even if a key is later compromised, past messages cannot be decrypted. The server never has access to plaintext after encryption.

The wire format is a JSON envelope:

```json
{
  "encrypted": true,
  "version": 1,
  "epk": "<base64 ephemeral public key>",
  "nonce": "<base64 24-byte nonce>",
  "ciphertext": "<base64 ciphertext>"
}
```

On receipt, the client calls `box.Open` with its static private key and the ephemeral public key to recover the original payload.

### Setting Up a Client Keypair

Generate a keypair once and store it locally:

```shell
gosmee keygen --key-file ~/.config/gosmee/key.json
```

This writes a `0600`-mode JSON file and prints the public key to stdout in base64 URL-safe format — paste that value into the server's channels config. Then pass the key file when starting the client:

```shell
gosmee client --encryption-key-file ~/.config/gosmee/key.json <server-url> <local-url>
```

Keep the key file private. Anyone with the private key can decrypt messages addressed to that keypair.

### Configuring the Server

Pass a channels config file to `gosmee server`:

```shell
gosmee server --encrypted-channels-file /etc/gosmee/channels.json
```

The config lists which channels are protected and which client public keys are authorized for each:

```json
{
  "channels": {
    "my-channel": {
      "allowed_public_keys": [
        "<base64-url public key from gosmee keygen>"
      ]
    }
  }
}
```

When a subscriber connects to a protected channel, the server checks their public key against the list. Unauthorized clients get a generic not-found response — no information about the channel is leaked. Channels not listed in the config remain normal plaintext channels.

### What Is and Isn't Encrypted

Encryption covers the **server-to-client SSE leg only**. Incoming webhook POST bodies arrive at the server in plaintext, as does all web UI traffic.

| Encrypted | Not encrypted |
|---|---|
| SSE payload delivery to authorized clients | Incoming webhook POST bodies |
| | Unlisted (plaintext) channels |
| | Web UI and `/new` endpoint |
| | TLS transport (use a reverse proxy) |

Encryption requires gosmee's own server — smee.io is not supported.

---

## Resource Protection

These controls protect server availability against large or malformed payloads.

### Payload Size Limits

gosmee enforces a 25 MB limit on incoming webhook bodies by default, matching GitHub's maximum. Raise or lower it with `--max-body-size` (in bytes):

```shell
gosmee server --max-body-size 10485760  # 10 MB
```

On the client side, the SSE receive buffer defaults to 1 MB. If you're forwarding large payloads, increase it to match:

```shell
gosmee client --sse-buffer-size 5242880 <SMEE_URL> <TARGET_URL>  # 5 MB
```

Raising these limits increases memory consumption proportionally. A server with a very high `--max-body-size` is also a more attractive DoS target. If you run gosmee in Kubernetes, update the memory `requests` and `limits` in your deployment manifests when you change these values, or Pods may be OOMKilled under load.

### Channel Name Length Limit

Channel names are capped at 64 characters across all endpoints. This guards against resource exhaustion from pathologically long names — no configuration is needed.

---

## Safe Command Execution

The `--exec` flag runs a shell command for each incoming webhook, with the payload written to `$GOSMEE_PAYLOAD_FILE` and headers to `$GOSMEE_HEADERS_FILE`. If you've already enabled signature validation and IP allowlisting, the scripts are much safer — but the payload content itself is still untrusted until your script validates it.

**The risk:** if your server accepts webhooks from untrusted sources and your exec script passes payload fields directly to shell commands (e.g. `$(jq -r .field)`), an attacker can craft a payload that executes arbitrary code.

**Mitigations:**

- Use `--webhook-signature` to verify that webhooks are from a trusted provider before they reach your script.
- Use `--allowed-ips` to restrict which hosts can send webhooks at all.
- In your scripts, treat all payload values as untrusted input — validate and sanitize before passing to any shell command.
- Use `--exec-on-events` to limit execution to specific event types, reducing attack surface.

---

## Operational Security

### Rotating Webhook Secrets

A restart is always required when rotating webhook secrets, regardless of whether you use `--webhook-signature` flags or the `GOSMEE_WEBHOOK_SIGNATURE` environment variable. The secret is read once at startup by the CLI flag parser and captured in a closure at route registration time — it is never re-read mid-run. In Kubernetes, `kubectl set env` on a Deployment triggers a pod restart, which picks up the new value; it is the restart doing the work, not live env-var re-reading.

The correct rotation procedure: add the new secret alongside the old one (gosmee accepts multiple values and tries each in turn, so in-flight webhooks signed with the old secret are not rejected), update the secret at your provider, then remove the old secret and restart once more.

### Rotating Encryption Keys

Generate a new keypair with `gosmee keygen`, add the new public key to the server's channels config file, and redistribute the new key file to clients out-of-band. Once all clients have switched to the new keypair, remove the old public key from the config. There is no built-in key rotation — this process is manual.

### What to Monitor in Server Logs

- **HTTP 403** from POST requests — IP allowlist rejections. A spike may indicate a scan or a misconfigured provider IP range.
- **HTTP 401** from POST requests — signature validation failures. Could indicate a misconfigured secret, a replay attempt, or an active forgery attempt.
- **Large payload rejections** — repeated hits against `--max-body-size` may indicate a DoS attempt.

### Kubernetes Considerations

When changing `--max-body-size` or `--sse-buffer-size`, update the memory `requests` and `limits` in your deployment manifests proportionally. Pods that exceed their memory limit are OOMKilled without warning.

---

## Reporting Vulnerabilities

Please report security issues by opening a [GitHub issue](https://github.com/chmouel/gosmee/issues). For sensitive disclosures, use GitHub's private vulnerability reporting feature on the Security tab.
