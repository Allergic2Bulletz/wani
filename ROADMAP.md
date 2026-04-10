# Wani — Implementation Roadmap

> Checkable task list for MVP implementation. Phases are roughly sequential but Phase 1 can overlap with early Phase 2 work. Each phase ends with a concrete verification milestone.
>
> See `architecture.md` for the decided architecture.

---

## Phase 0: Foundation — Server + CI/CD

*Goal: Deployable server binary with automated build/deploy pipeline.*
*Decisions: 1 (Go), 9 (single binary), 10 (core lib + CLI), 11 (Identity interface), 13 (VM + CI/CD)*

- [ ] Initialize Go module (`go mod init`)
- [ ] Scaffold project directory layout:
  - `cmd/wani-server/main.go` — server entrypoint
  - `cmd/wani-client/main.go` — client entrypoint
  - `internal/server/` — server logic
  - `internal/client/` — client core library
  - `internal/protocol/` — shared types (messages, manifest, pairing codes)
  - `internal/identity/` — `Identity` interface + `EphemeralIdentity` stub
- [ ] Minimal wani-server: HTTP health check endpoint (`GET /health` → 200 OK)
- [ ] Provision cloud VM (DigitalOcean $5/mo or Oracle Cloud Free Tier)
  - Open ports: TCP 443 (WebSocket signaling), UDP range for TURN (when enabled)
- [ ] Manual SCP deploy to VM, verify health endpoint via `curl`
- [ ] GitHub Actions workflow: **build → SCP → restart** on push to `main`
  - Trigger: `on: push: branches: [main]`
  - Steps: checkout → `go build -o wani-server ./cmd/wani-server` → SCP binary to VM → SSH restart
  - Secrets: `VM_SSH_KEY`, `VM_HOST`, `VM_USER`
- [ ] systemd unit file for wani-server (auto-restart on crash, stdout → journald)

**Verification:** `curl http://<vm-ip>:<port>/health` returns 200. Push a trivial change to `main` → binary is rebuilt and restarted automatically within a few minutes.

---

## Phase 1: STUN Service

*Goal: Client can discover its public IP:port for NAT traversal.*
*Decision: 3 (Full ICE — STUN is the first step)*

- [ ] wani-client STUN query: use public STUN servers (`stun.cloudflare.com:3478`, `stun.l.google.com:19302`) to resolve server-reflexive candidate (public IP:port)
- [ ] CLI command: `wani-client stun` — prints discovered public address
- [ ] Handle STUN failure gracefully (timeout, unreachable) — report error, don't crash
- [ ] Test: run from behind NAT, verify correct public IP is returned

**Verification:** `wani-client stun` prints correct public IP when run from behind home NAT. Run from cloud VM → prints VM's public IP (no NAT).

---

## Phase 2: WebSocket Signaling + SPAKE2

*Goal: Two clients find each other via pairing code and complete SPAKE2 key exchange.*
*Decisions: 5 (WebSocket), 6 (SPAKE2 Option B), 7 (4-word codes)*

### 2a: WebSocket Signaling Server
- [ ] WebSocket upgrade endpoint in wani-server (`/ws`)
- [ ] Session/room model: sender creates session → gets 4-word pairing code; receiver joins with code
- [ ] Message types: `create_session`, `join_session`, `relay` (opaque payload forwarding)
- [ ] Session cleanup: auto-expire sessions after timeout (e.g., 5 minutes)
- [ ] Error handling: duplicate session, invalid code, session expired

### 2b: Pairing Code Generator
- [ ] Curated wordlist (~2048 words → 11 bits per word → 4 words = 44 bits)
- [ ] Code format: `word-word-word-word` (lowercase, hyphen-separated)
- [ ] Cryptographically random word selection (`crypto/rand`)
- [ ] Place wordlist in `internal/protocol/` — embedded via `//go:embed`

### 2c: SPAKE2 Key Exchange
- [ ] Integrate Go SPAKE2 library (RFC 9382 implementation)
- [ ] Client flow: both peers derive SPAKE2 messages from pairing code → relay via signaling → derive shared secret K
- [ ] Verify: both clients derive identical K from the same pairing code
- [ ] Reject: mismatched K (wrong code) produces an error, connection is dropped

### 2d: Ping-Pong Milestone
- [ ] After SPAKE2 completes, send authenticated message through signaling WebSocket
- [ ] Message: `HMAC-SHA256(K, "wani-ping")` → peer verifies → responds with `HMAC-SHA256(K, "wani-pong")`
- [ ] This proves: signaling works, pairing code works, SPAKE2 works, identity verification works
- [ ] **This is a demo-able milestone** — two terminals, type a code, see "Paired successfully!"

### 2e: ICE Candidate Exchange
- [ ] Extend WebSocket protocol: `ice_candidate` message type
- [ ] Sender/receiver trickle ICE candidates through signaling server
- [ ] Server relays candidates opaquely (doesn't parse SDP)

**Verification:** Two clients (can be on same machine for now) pair via 4-word code, complete SPAKE2, display "Paired!" after authenticated ping-pong. Wrong code → "Pairing failed."

---

## Phase 3: P2P QUIC Connection + File Transfer

*Goal: Two clients establish a direct QUIC connection via ICE and transfer files.*
*Decisions: 2 (QUIC), 3 (ICE), 8 (manifest-first, per-file resume, xxHash), 12 (encryption layering)*

### 3a: ICE + QUIC Connection
- [ ] Integrate `pion/ice`: create ICE agent, gather candidates (host + server-reflexive)
- [ ] Trickle candidates to peer via signaling (Phase 2e)
- [ ] ICE connectivity checks → select best candidate pair
- [ ] Establish QUIC connection over ICE-selected UDP path (`quic-go` over `pion/ice` `net.Conn`)
- [ ] HMAC identity proof: first QUIC stream message is `HMAC-SHA256(K, "wani-quic-verify")` — peer verifies before allowing file transfer

### 3b: Manifest Protocol
- [ ] Sender scans file tree → builds manifest: `[]FileEntry{Path, Size, XXHash, Compression: "none"}`
- [ ] Compute xxHash per file during scan (`github.com/cespare/xxhash/v2`)
- [ ] Send manifest over QUIC control stream (stream 0)
- [ ] Receiver parses manifest → creates directory structure → sends `ready` response

### 3c: File Data Transfer
- [ ] Sender streams each file over a QUIC stream (one stream per file, or sequential on a data stream)
- [ ] Receiver writes to disk → computes xxHash → compares against manifest → marks file `complete`
- [ ] Handle xxHash mismatch: report error, mark file for retry

### 3d: Per-File Resume
- [ ] On reconnect: sender sends manifest again; receiver responds with list of already-completed files
- [ ] Sender skips completed files, resumes from next `pending` file
- [ ] Resume state stored client-side (e.g., `.wani-resume.json` alongside received files)

### 3e: CLI UX
- [ ] Sender: `wani-client send <path>` → displays pairing code → waits → shows progress
- [ ] Receiver: `wani-client receive <code>` → pairs → displays manifest → transfers → done
- [ ] Progress bar: `[####----] 45% | 450MB/1GB | file 3/7 | eta 12s`

**Verification:** Transfer a directory of mixed files (small + large) between two machines on different networks via wani-server. Verify xxHash matches on all files. Kill mid-transfer, restart with same code → resumes from next incomplete file.

---

## Phase 4: TURN Relay (Stretch Goal)

*Goal: Transfers work even when direct P2P fails.*
*Decisions: 4 (TURN + TCP fallback), 9 (pion/turn embedded)*

### 4a: TURN Server
- [ ] Integrate `pion/turn` into wani-server behind `--relay` flag
- [ ] Per-session HMAC TURN credentials (scoped to SPAKE2 session, auto-expire when transfer ends)
- [ ] wani-server issues TURN credentials to clients during signaling (new message type: `turn_credentials`)

### 4b: Client Relay Support
- [ ] ICE agent includes relay candidates from TURN server
- [ ] QUIC runs identically over relay path — no client code changes needed
- [ ] Test: force relay path (e.g., block direct UDP between peers), verify transfer still works

### 4c: TCP Relay Fallback (Last Resort)
- [ ] If UDP to TURN is also blocked, relay file data through WebSocket signaling connection
- [ ] Lower performance (HTTP framing overhead) but works through any firewall
- [ ] This is the absolute last resort — log a warning when this path is used

**Verification:** Transfer works when one client is behind symmetric NAT (relay path). Transfer works when UDP is completely blocked (TCP WebSocket fallback).

---

## Summary

| Phase | Milestone | Dependencies |
|-------|-----------|-------------|
| **0** | Health endpoint live on VM; CI/CD auto-deploys | — |
| **1** | `wani-client stun` shows public IP | — |
| **2** | Two clients pair + SPAKE2 + authenticated ping-pong | Phase 0 (server running) |
| **3** | End-to-end file transfer over QUIC/ICE with resume | Phase 1 + Phase 2 |
| **4** | Transfer works via relay when P2P fails | Phase 3 + Phase 0 (`--relay`) |
