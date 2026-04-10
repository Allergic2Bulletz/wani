# Wani — Project Overview

> **Wani** (Japanese for alligator) is a plug-and-play file sharing application. Clients send files to each other directly using NAT traversal / hole punching, falling back on a relay server when a direct connection cannot be established. Identity is established via key exchanges between clients.

---

## Ecosystem Components

### 1. wani-server (Central Server)

| Aspect | Detail |
|--------|--------|
| **Role** | Central coordination point for anonymous file transfers |
| **Services** | WebSocket signaling; STUN (uses public servers: `stun.cloudflare.com`); built-in TURN relay via `pion/turn` (enabled with `--relay` flag) |
| **Storage** | No file data stored permanently — relay only |
| **Users** | Anonymous clients; no accounts required |
| **Deployment** | Single Go binary; deployed to cloud VM via SCP; CI/CD via GitHub Actions |

**Responsibilities:**
- WebSocket signaling / rendezvous between sender and receiver
- SPAKE2 message relay: forward key exchange messages between paired clients (transparent, cannot derive key)
- ICE candidate trickle relay: forward ICE candidates for NAT traversal
- STUN: clients use public STUN servers to discover their public IP/port for hole punching
- TURN relay (when `--relay` enabled): relay encrypted QUIC traffic for peers behind symmetric NAT; per-session HMAC credentials scoped to SPAKE2 session
- Session management (ephemeral rooms, 4-word pairing codes)

### 2. wani-client

| Aspect | Detail |
|--------|--------|
| **Role** | End-user application for sending/receiving files |
| **Architecture** | Core library (`internal/client/`) + CLI frontend (`cmd/wani-client/`); REST daemon migration planned for GUI |
| **Interface** | CLI first; clean simple GUI in the longer term |
| **Identity** | SPAKE2 key exchange from 4-word pairing code; abstract `Identity` interface for future persistent keypairs (ponds) |

**Responsibilities:**
- Generate and share 4-word human-pronounceable pairing codes (~33-44 bits entropy)
- SPAKE2 key exchange over signaling WebSocket → derive shared secret K
- Full ICE candidate gathering via `pion/ice` (host + server-reflexive + relay candidates)
- Establish QUIC connection over ICE-selected UDP path via `quic-go`
- HMAC identity proof: first QUIC message verifies peer completed same SPAKE2 exchange
- Manifest-first file transfer: scan tree → build manifest (path, size, xxHash per file) → send manifest → stream files
- Per-file integrity verification via xxHash after write to disk
- Per-file resume: track `pending`/`complete` per file; restart at next incomplete file
- Display transfer progress (bytes/files transferred)

### 3. wani-pond (Long-Term Goal)

| Aspect | Detail |
|--------|--------|
| **Role** | Self-hosted community file sharing server |
| **Model** | Like wani-server, with additional features for persistent groups |
| **Key Feature** | Asynchronous file sharing — files stored on the pond for later retrieval |
| **Target Users** | Small teams of developers, friend/family groups |
| **Self-Hosting** | Users host their own pond and invite trusted people |

**Planned Features:**
- All wani-server functionality (STUN + relay)
- Persistent file storage for asynchronous sharing
- Invite system for adding members to a pond
- Basic security settings at pond creation:
  - Whether users can send files directly to each other
  - Whether users can invite other people into the pond
- Limited management features initially (small-group focus)

---

## Core Requirements

| Requirement | Description |
|-------------|-------------|
| **Plug-and-Play** | Minimal setup; no accounts, no port forwarding, no complex configuration |
| **NAT Traversal** | Direct P2P via hole punching (STUN-assisted) as primary path |
| **Relay Fallback** | Transparent relay through wani-server when direct P2P fails |
| **End-to-End Encryption** | Key exchange between clients; server cannot read file data |
| **Identity via Key Exchange** | No central auth; clients prove identity through cryptographic key exchange |
| **No Permanent Storage** | wani-server stores nothing; data flows through or is relayed in real-time |
| **Cross-Platform** | Should work across major OSes |
| **CLI-First** | Command-line interface as primary; GUI as future enhancement |

---

## Prior Art

Three existing projects that solve similar problems and inform wani's design:

| Project | Language | NAT Strategy | Encryption | Key Insight |
|---------|----------|-------------|------------|-------------|
| [croc](https://github.com/schollz/croc) | Go | Relay-only (no hole punching) | PAKE (custom SIEC curve) | Simplicity + reliability; single binary; relay-first trades speed for 100% NAT compat |
| [magic-wormhole](https://github.com/magic-wormhole/magic-wormhole) | Python | Direct TCP + relay fallback | SPAKE2 (RFC 9382) + NaCl | Clean layered protocol; mailbox server for signaling; rich ecosystem of reimplementations |
| [Thruflux](https://github.com/samsungplay/Thruflux) | C++ | Full ICE (STUN/TURN via libnice) | QUIC TLS 1.3 | Performance-first; QUIC native multiplexing; true P2P hole punching; manifest-first design |

Detailed research on each: [croc_research.md](croc_research.md), [wormhole_research.md](wormhole_research.md), [thruflux_research.md](thruflux_research.md)

---

## Comparison: Architectural Choices Across Prior Art

| Dimension | Croc | Magic-Wormhole | Thruflux | **Wani Consideration** |
|-----------|------|----------------|----------|------------------------|
| **Language** | Go | Python | C++ | **Go** — single static binary, fast dev, `quic-go` + `pion/ice` + `pion/turn` ecosystem |
| **Transport** | TCP (multi-port) | TCP | QUIC (UDP) | **QUIC** — reliable + ordered + encrypted + multiplexed over UDP; NAT hole-punch friendly |
| **NAT Strategy** | Relay-only; local multicast discovery | Direct TCP + Transit Relay fallback | Full ICE (STUN → hole punch → TURN fallback) | **Full ICE** via `pion/ice` — STUN → hole punch → TURN fallback → TCP relay last resort |
| **Signaling** | Relay server rooms | Mailbox server (WebSocket + SQLite) | WebSocket signaling server | **WebSocket**, stateless — signaling + SPAKE2 relay + ICE candidate trickle |
| **Key Exchange** | PAKE (custom, textbook-based) | SPAKE2 (RFC 9382, standardized) | None (QUIC TLS 1.3 handles crypto; join code = session proof) | **SPAKE2 (RFC 9382)** + QUIC TLS Option B — HMAC identity proof inside first QUIC message |
| **Encryption** | Symmetric (PAKE-derived key) | NaCl SecretBox per-phase + Noise (dilation) | QUIC TLS 1.3 (AEAD, PFS built-in) | **QUIC TLS 1.3** for transport + **SPAKE2** for identity verification (not double encryption) |
| **Code/Pairing** | 6+ char human-pronounceable phrase | `<nameplate>-<word1>-<word2>` (~16 bits) | 16-char alphanumeric (~95 bits) | **4 human-pronounceable words** (~33-44 bits); configurable via `--words N` |
| **Relay Design** | Multi-port TCP relay, ephemeral rooms | Separate mailbox + transit relay servers | TURN (coturn) for data; WebSocket for signaling | **Single binary**: signaling + `pion/turn` relay behind `--relay` flag; coturn migration planned |
| **Resume Support** | Yes (chunk-based) | No (basic); planned via Dilation | Yes (manifest-based, per-file) | **Manifest-first, per-file resume** for MVP; per-chunk upgrade planned |
| **Multi-Receiver** | No (1:1) | No (1:1) | Yes (up to 10) | 1:1 for MVP; multi-receiver deferred to ponds |
| **Self-Hosting** | `croc relay` (trivial) | Separate mailbox + transit servers | `thru server` + coturn | Single Go binary; SCP to VM; self-hosting trivial |
| **GUI** | None (CLI only) | Warp (GNOME GUI, separate project) | Electron desktop app + REST API | **Core library + CLI** for MVP; REST daemon migration planned for GUI |
| **Performance (10GB)** | 2m 40s | 22m 20s | 2m 20s (P2P) | QUIC/UDP; expect performance in Thruflux range |

---

## Key Patterns to Consider for Wani

### Adopted
- **Full ICE NAT traversal** (Thruflux model) — `pion/ice` for STUN discovery + hole punching + TURN fallback + TCP relay last resort.
- **SPAKE2 (RFC 9382) for code-based pairing** (Wormhole model) — 4-word human-pronounceable codes → strong session key. Identity proof via HMAC inside first QUIC message (Option B).
- **QUIC transport** (Thruflux model) — `quic-go` for reliable, encrypted, multiplexed UDP. NAT-hole-punch friendly. QUIC TLS 1.3 handles transport encryption.
- **Manifest-first file transfer** (Thruflux model) — send file metadata + xxHash per file before data. Enables per-file resume, pre-allocation, early abort, accurate progress.
- **Per-file xxHash integrity** — belt-and-suspenders with QUIC AEAD. Catches disk write errors after the QUIC layer.
- **Stateless relay** — relay server as transparent encrypted pipe, no storage, no decryption capability.
- **Self-hostable single binary** — single Go binary for server; SCP to VM; `--relay` flag enables TURN.
- **Core library + CLI** — `internal/client/` has zero terminal I/O; CLI is a thin consumer. REST daemon migration planned for GUI.
- **Abstract Identity interface** — `EphemeralIdentity` (SPAKE2) for MVP; `PersistentIdentity` (Ed25519) for ponds.

### Avoided
- **Relay-only architecture** (Croc model) — sacrifices direct P2P speed.
- **Python/heavy runtime dependencies** (Wormhole model) — installation friction; prefer compiled single binary.
- **Low-entropy pairing codes** (Wormhole's ~16 bits) — vulnerable to offline guessing against SPAKE2 messages.
- **REST API for MVP** (Thruflux model) — over-engineered for CLI-only phase; deferred to GUI migration.
- **Compression for MVP** — protocol reserves a `compression` field for future use; no implementation now.

### Deferred to Ponds
- **Noise protocol for PFS** — if persistent pond connections need PFS beyond QUIC session resumption.
- **Persistent Ed25519 identity** — trust-on-first-use model for pond membership.
- **Pond-specific patterns** — none of the three reference tools have an analog. Research Syncthing, Seafile, or similar when pond work begins.

---

## Implementation Phases (MVP)

See `ROADMAP.md` for the full checkable task list.

| Phase | Goal | Key Deliverable |
|-------|------|----------------|
| **0** | Foundation | Go scaffold + wani-server on VM + GitHub Actions CI/CD |
| **1** | STUN | Client discovers public IP:port via `wani-client stun` |
| **2** | Signaling + SPAKE2 | Two clients pair via 4-word code, complete SPAKE2, exchange authenticated ping-pong |
| **3** | P2P Transfer | QUIC over ICE, manifest-first file transfer with xxHash + per-file resume |
| **4** | TURN Relay (stretch) | Transfers work behind symmetric NAT and UDP-blocked networks |

**Deferred:**
- wani-pond (persistent storage, invites, group management, Ed25519 identity)
- GUI client (REST daemon migration)
- Multi-receiver support
- Compression (protocol field reserved)
