You're correct — IP-in-binary is not a meaningful attack surface expansion. Anyone doing a `tcpdump` on their own machine sees the server IP immediately.

Here's a practical threat model and defense stack for your use case:

---

## Threat 1: DDoS / Resource exhaustion

The signaling server's attack surface is small but real: an attacker could hammer `/ws` or `POST /create_session` to exhaust memory with open sessions.

**Application-level mitigations** (most important):

- **Max WebSocket message size** — gorilla lets you set `conn.SetReadLimit(bytes)`. Without it, a single connection can stream a giant payload and exhaust RAM. A few KB is plenty for signaling messages.
- **Rate-limit session creation** — the `SessionStore` has no cap. A simple `maxSessions` constant + rejecting `CreateSender` when exceeded is sufficient. A few hundred concurrent sessions is generous.
- **Connection limit per IP** — track active WS connections per remote IP; reject beyond N. This blunts the simplest DDoS pattern.

The session TTL you already have (`sessionTTL = 5 * time.Minute`) is excellent and already prevents slow accumulation.

**Infrastructure-level mitigations:**

- Put **nginx or Caddy** in front as a reverse proxy. Both have `limit_conn` (concurrent connections per IP) and `limit_req` (request rate) built in. This handles most volumetric attacks before your Go process even sees them.
- Your VPS provider almost certainly has **network-layer DDoS scrubbing** (Hetzner, DigitalOcean, Linode all do basic protection for free). It won't stop a serious L7 attack but it handles amplification floods.

---

## Threat 2: VM compromise

**OS hardening checklist for your systemd-managed server:**

- **SSH**: key-only auth (`PasswordAuthentication no` in `/etc/ssh/sshd_config`), disable root login (`PermitRootLogin no`), consider moving off port 22.
- **firewall (ufw)**: allow only what's needed. Likely: `22/tcp` (SSH), `8080/tcp` (wani-server signaling), and `3478/udp+tcp` (STUN/TURN when you implement the relay). Deny everything else.
- **Principle of least privilege**: run `wani-server` as a dedicated non-root user (your `.service` file should have `User=` and `NoNewPrivileges=true`).
- **Automatic security updates**: `unattended-upgrades` on Debian/Ubuntu handles kernel and library CVEs without manual intervention.
- **fail2ban**: protects SSH from brute-force. Configured in 10 minutes.

---

## Current code items worth noting

In ws.go, the `CheckOrigin: return true` TODO is fine. Since wani-client is a CLI, there's no browser origin to validate — this comment can just be removed.

The `/health` endpoint in server.go echoes the listen address. That's a minor info leak — could just return `"ok"` instead of `"Server is listening on " + s.addr`.

---

## What won't help much

- Obscuring the IP (as you concluded)
- TLS on the signaling WebSocket prevents eavesdropping of pairing codes in transit, but your SPAKE2 key exchange already protects the actual transfer content — the signaling TLS is more about protecting the pairing code itself from network sniffers before the QUIC session is established. Worth adding eventually, but not urgent.

The realistic threat for a small tool like this is script kiddies running scanners, not targeted attacks. The nginx rate limiting + ufw firewall covers the vast majority of that.