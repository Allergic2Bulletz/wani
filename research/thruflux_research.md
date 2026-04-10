# Thruflux — Research Document

> **Source:** [GitHub](https://github.com/samsungplay/Thruflux) | README | Source structure analysis | Benchmarks
> **Author:** @samsungplay
> **License:** MIT

---

## Overview & Philosophy

Thruflux is a modern, cross-platform P2P file transfer toolkit written in C++17. It prioritizes **maximum throughput** using the QUIC protocol (UDP-based) with built-in encryption, automatic NAT traversal via ICE (STUN/TURN), and optional relay fallback. Designed as both a desktop GUI application and CLI utility with no signup required.

Key design philosophy:
- **Speed first** — QUIC/UDP native transport; no compression overhead; parallel stream multiplexing
- **P2P-centric** — Direct transfer preferred; server is blind to file content; relay only when necessary
- **Simple by default** — No accounts, no configuration; automatic TURN fallback; high-entropy join codes

---

## Tech Stack

| Component | Detail |
|-----------|--------|
| **Language** | C++17 (63.9% of codebase) |
| **Frontend** | TypeScript (21.2%) — Electron-based desktop GUI |
| **Build System** | CMake 3.24+ with vcpkg package management |
| **Compilers** | GCC 10+ (Linux), MSVC 2022 (Windows), Xcode CLI Tools (macOS) |

### Core Libraries

| Library | Purpose |
|---------|---------|
| **LSQUIC** (≥4.6.0) | QUIC protocol implementation (UDP transport) |
| **libnice** | ICE (STUN/TURN) NAT traversal |
| **uwebsockets** | Server-side WebSocket (signaling server) |
| **ixwebsocket** (+ MbedTLS) | WebSocket client |
| **OpenSSL 3.6.1** | TLS/crypto operations |
| **MbedTLS** | Alternative TLS/crypto |
| **nlohmann_json** | JSON serialization |
| **CLI11** | Command-line argument parsing |
| **Boost.ASIO** | Async I/O operations |
| **Boost.URL** | URL parsing |
| **spdlog** | Logging |
| **llfio** | Low-level file I/O |
| **cpp-httplib** (≥0.30.2) | Local HTTP server for UI REST API |
| **indicators** | Terminal progress bars |

---

## End-to-End Process Flow

### Phase 1: Initialization & Discovery

```
Sender: thru host ./files
  → Scans files/directories
  → Builds manifest (metadata: file list, sizes, structure)
  → Connects to signaling server via WebSocket (WSS)
  → Requests join code from server
  → Server generates 16-digit alphanumeric join code (~95 bits entropy)
  → Returns join code to sender
```

### Phase 2: Receiver Joins

```
Receiver: thru join <join-code>
  → Connects to signaling server with join code
  → Server validates code against active sessions
  → Receives sender's ICE candidates via WebSocket
```

### Phase 3: ICE Negotiation (NAT Traversal)

```
Sender & Receiver (parallel, via signaling server):
  → Each gathers local candidates using STUN server (default: stun.cloudflare.com:3478)
  → Exchange ICE candidates through WebSocket signaling
  → Attempt direct P2P UDP path (hole punching)
  → If direct fails → automatic fallback to TURN relay
```

### Phase 4: QUIC Handshake & Manifest Transfer

```
After P2P or relay path established:
  → QUIC handshake (TLS 1.3 encryption built into QUIC)
  → Receiver downloads manifest stream first
    → File list, directory structure, sizes
    → Pre-allocate disk space, validate structure
    → Track progress for resumable transfers
```

### Phase 5: File Data Transfer

```
  → Files sent via QUIC streams (uncompressed, as-is)
  → Multi-stream parallelization for throughput
  → Built-in flow control and congestion management (QUIC native)
  → Per-file resumability if connection interrupts
```

### Phase 6: Completion

```
  → QUIC provides end-to-end integrity verification (AEAD per frame)
  → Receiver confirms all files received
  → Session expires after default 24 hours (86400s)
```

### Architecture Diagram

```
┌──────────────────────────────────────────────────────────────┐
│                      THRUFLUX SYSTEM                         │
├──────────────────────────────────────────────────────────────┤
│                                                              │
│  [SIGNALING SERVER]  ←── WSS ──→  [SENDER CLI]              │
│  - ICE candidate relay             - Manifest build          │
│  - Session management              - Stream management       │
│  - Join code generation            - Rate control            │
│  - Rate limiting                   - Multi-receiver          │
│                                                              │
│  [SIGNALING SERVER]  ←── WSS ──→  [RECEIVER CLI]            │
│                                    - Manifest parsing        │
│                                    - File reconstruction     │
│                                    - Resume tracking         │
│                                                              │
│  [P2P PATH]  ←── QUIC/UDP ──→    Direct connection          │
│  (ICE negotiated)                  (preferred)               │
│                                                              │
│  [TURN RELAY]  ←── QUIC/UDP ──→  Fallback only              │
│  (coturn compatible)               (if P2P fails)            │
│                                                              │
│  [FRONTEND]  ←── REST/SSE ──→    [Local UI Engine]          │
│  (Electron desktop)                /host, /receive, /events  │
│                                                              │
└──────────────────────────────────────────────────────────────┘
```

### Source Module Structure

| Module | Purpose |
|--------|---------|
| `server/` | Signaling server: ServerSocketHandler, TransferSession, SessionTracker |
| `sender/` | Sender engine: SenderEntryPoint, SenderStream, manifest building |
| `receiver/` | Receiver engine: ReceiverEntryPoint, ReceiverStream, manifest parsing |
| `common/` | Shared: IceHandler, Stream, Payloads, Types, TTLCache, ThreadManager |
| `ui/` | Local web interface: UIEntryPoint, REST API handlers |
| `frontend/` | Desktop GUI (TypeScript/Electron) |
| `app/` | CLI entry point: Main.cpp — dispatcher for `thru server|host|join|ui` |

---

## NAT Traversal Strategy

### ICE (Interactive Connectivity Establishment)

**Library:** libnice (mature, proven NAT traversal)

#### Process

1. **STUN Discovery**
   - Each side contacts STUN server (default: `stun.cloudflare.com:3478`)
   - Obtains external IP:port mapping from ISP/router
   - Discovers local IP addresses

2. **Candidate Gathering**
   - Host candidates (local IPs)
   - Server reflexive candidates (STUN-derived external IPs)
   - Peer reflexive candidates (discovered during connectivity checks)

3. **Candidate Exchange**
   - ICE candidates exchanged via WebSocket signaling server
   - Both endpoints attempt to reach each other directly (UDP hole punching)

4. **TURN Fallback**
   - Only triggered if direct P2P connectivity fails
   - QUIC-over-UDP through TURN relay
   - Performance degrades based on relay capacity

### Supported NAT Types

| NAT Type | Support | Method |
|----------|---------|--------|
| Full cone | Direct P2P | Hole punching |
| Address-restricted cone | Direct P2P | Hole punching |
| Port-restricted cone | Direct P2P | Hole punching |
| Symmetric NAT | Via TURN relay | Relay fallback |
| Double/Nested NAT | Via TURN relay | Relay fallback |

### Configuration

```bash
# Default STUN
--stun-server stun://stun.cloudflare.com:3478

# Custom TURN (UDP only)
--turn-server turn://user:pass@turn.example.com:3478

# Force relay mode (testing)
--force-turn
```

---

## Encryption & Key Exchange

### Transport Security (QUIC)

QUIC has TLS 1.3 built into the protocol — encryption is not optional.

| Phase | Encryption |
|-------|-----------|
| Initial packets | AES-128-GCM (hardcoded) |
| Handshake | TLS-derived keys |
| Application data | HKDF-derived session keys |
| Key rotation | Periodic (Perfect Forward Secrecy) |

**Key exchange flow:**
1. QUIC Initial packets exchanged
2. TLS 1.3 certificate exchange embedded in QUIC handshake
3. Ephemeral session keys derived via HKDF
4. Application data encrypted with session keys

### Signaling Security

- WebSocket Secure (WSS) with TLS 1.2+
- Self-hosting: Caddy reverse proxy provides auto-HTTPS

### Join Code Security

| Property | Detail |
|----------|--------|
| **Format** | 16-character alphanumeric |
| **Entropy** | ~95 bits (cryptographically secure random) |
| **Verification** | Receiver must supply exact code to server |
| **Brute-force resistance** | 95 bits makes guessing infeasible |

**Important difference from croc/wormhole:** No PAKE/SPAKE2. The join code is a session identifier, not a cryptographic input. QUIC TLS 1.3 handles all encryption. The join code proves "I was told about this session" but does not contribute to key material.

### Data Integrity

- Each QUIC frame includes AEAD authentication tag
- Packet reordering/tampering detected automatically
- Corrupted frames dropped; sender retransmits
- Manifest integrity implicit in QUIC frame processing

---

## Relay Server Design

### Signaling Server

| Property | Detail |
|----------|--------|
| **Protocol** | WebSocket (uwebsockets) |
| **Default** | `bytepipe.app` (WSS) |
| **Role** | ICE candidate exchange, session management, join code generation |
| **File access** | NONE — server never sees file data |
| **State** | Ephemeral sessions; no database |

### TURN Relay (coturn)

| Property | Detail |
|----------|--------|
| **When used** | Only if direct P2P fails (restrictive NATs, corporate firewalls) |
| **Default** | bytepipe.app relay (~900 Mbps shared, fair-use, ~2000 concurrent users) |
| **Protocol** | UDP only (QUIC requires UDP) |
| **Credentials** | Server auto-generates time-limited REST API credentials (TTL: 600s default) |

**Important:** TURNS (TLS over TURN) not yet supported.

### Self-Hosted Deployment

**Signaling server:**
```bash
./thru server \
  --port 8080 \
  --max-sessions 1000 \
  --max-receivers-per-sender 10 \
  --session-timeout 86400
```

**With Caddy (TLS termination):**
```
# /etc/caddy/Caddyfile
your.domain {
  reverse_proxy localhost:8080
}
```

**With coturn TURN:**
```bash
./thru server \
  --turn-server turn://your.domain:3478 \
  --turn-static-auth-secret your-secret \
  --turn-cred-ttl 600
```

Clients automatically get TURN credentials from signaling server — no client-side TURN configuration needed.

### Rate Limiting (Server)

| Parameter | Default | Purpose |
|-----------|---------|---------|
| `ws-connections-per-min` | 30 | New WS connections per minute |
| `ws-connections-burst` | 10 | Burst allowance |
| `ws-messages-per-sec` | 50 | Messages per second |
| `ws-messages-burst` | 100 | Message burst |
| `max-ws-connections` | 2000 | Total concurrent WebSocket connections |
| `max-sessions` | 1000 | Concurrent transfer sessions |
| `max-message-bytes` | 65536 | Max WebSocket message size (64 KB) |

---

## Code Sharing / Pairing Mechanism

### Join Code Lifecycle

```
1. Sender: thru host ./files
   → Server generates: ALPHA-BRAVO-0123-4567-89AB-CDEF

2. User shares code manually (chat, email, QR, voice — out-of-band)

3. Receiver: thru join ALPHA-BRAVO-0123-4567-89AB-CDEF
   → Server validates code vs active sessions
   → If valid: receiver gets ICE candidates for that sender
```

### Properties

| Property | Detail |
|----------|--------|
| Entropy | ~95 bits (16 alphanumeric chars) |
| Generation | Server-side cryptographically secure random |
| Multi-receiver | Up to 10 per sender (configurable) |
| Session timeout | 24 hours (86400s default) |
| Sharing mechanism | None built-in (intentional for privacy) |

### Multi-Receiver Support

- Up to 10 receivers per sender (default, configurable via `--max-receivers-per-sender`)
- Each receiver gets independent QUIC connection
- Server tracks receiverId for each participant

---

## Manifest-First Architecture

A key differentiator of Thruflux's design:

### What is the Manifest?

A metadata blob sent **before** any file data containing:
- Complete file list with paths
- Directory structure
- File sizes
- Total transfer size

### Benefits

| Benefit | Detail |
|---------|--------|
| **Early validation** | Receiver can verify structure before downloading |
| **Pre-allocation** | Receiver can allocate disk space upfront; abort if insufficient |
| **Resumable transfers** | Track per-file completion; skip already-received files |
| **Progress tracking** | Accurate progress bars from total known size |
| **Quick abort** | Receiver can reject before data transfer begins |

### Limitations

- Slight overhead for small transfers (metadata before data)
- File-level resume only (not byte-offset within a file)
- Very large directory trees may approach QUIC stream limits

---

## Design Decisions & Trade-offs

### 1. QUIC/UDP-Only (No TCP Fallback)

- **Rationale:** UDP enables hole punching; QUIC provides multiplexing + encryption + congestion control
- **Pro:** Eliminates TCP overhead (slow start, head-of-line blocking)
- **Con:** Won't work on networks that block UDP (rare enterprise firewalls)

### 2. No File Compression

- **Rationale:** Compression is CPU-bound; on modern networks, raw transfer is faster than compress-then-transfer
- **Pro:** Simpler pipeline, faster throughput, user retains control
- **Con:** Text-heavy files transfer as larger data volume

### 3. Manifest-First Design

- **Rationale:** Metadata before data enables validation, resume, pre-allocation
- **Pro:** Robust resume support; receiver can abort early
- **Con:** Slight latency for small transfers

### 4. Single QUIC Connection Per Receiver

- **Rationale:** One CC algorithm, efficient resource use, simpler multiplexing
- **Pro:** Better congestion control; fewer port bindings
- **Con:** Mitigated by QUIC's native stream multiplexing

### 5. Automatic TURN Fallback

- **Rationale:** "Just works" for restrictive networks; server auto-provisions credentials
- **Pro:** No manual configuration needed on client side
- **Con:** Performance degrades on relay; shared bandwidth

### 6. No PAKE/SPAKE2

- **Rationale:** QUIC TLS 1.3 handles encryption; join code is session proof, not crypto input
- **Pro:** Simpler protocol; one encryption layer instead of two
- **Con:** Join code doesn't contribute to key material; identity based on "knows the code" not cryptographic proof

### 7. Stateless Signaling Server

- **Rationale:** Minimal server load; file data never touches server; scales well
- **Pro:** Privacy, fault tolerance, easy horizontal scaling
- **Con:** More complex receiver flow (must connect to peer after ICE)

### 8. REST API for UI Integration

- **Rationale:** Decouple CLI/GUI from transfer engine
- **Pro:** Multiple frontend options (CLI, Electron, web); clean separation
- **Con:** Extra HTTP server process for GUI mode

---

## Performance Characteristics

### Benchmarks (Median of 3 runs)

**Environment:** Chicago → Seoul, Vultr 2vCPU AMD EPYC, 4GB RAM, NVMe, high-latency internet path

| Tool | Transport | P2P? | Resumable? | 10 GB Transfer | 10k Files Transfer |
|------|-----------|------|------------|---------------|-------------------|
| **Thruflux (P2P)** | QUIC/UDP | Yes | Yes | **2m 20s** | **2m 18s** |
| **Thruflux (TURN)** | QUIC/UDP relay | No | Yes | 5m 40s | 5m 35s |
| Croc | TCP | Yes | Yes | 2m 40s | 19m 33s |
| Wormhole | TCP relay | No | No | 22m 20s | N/A (stalled) |
| SCP | TCP | Yes | No | 15m 06s | 26m 14s |
| Rsync | TCP | Yes | Yes | 15m 18s | 14m 53s |

### Key Insights

- QUIC native multiplexing is **dramatically** faster for many small files (2m 18s vs 19m 33s for croc)
- P2P QUIC competitive with TCP relay for single large files
- TURN relay adds ~3m 20s overhead due to bandwidth sharing
- QUIC's congestion control handles high-latency links well

### Tuning Parameters

| Parameter | Default | Purpose |
|-----------|---------|---------|
| QUIC connection window | 256 MB | Flow control window (connection-level) |
| QUIC stream window | 32 MB | Flow control window (per-stream) |
| UDP buffer | 8 MB (installer raises to 16 MB) | OS-level UDP buffer; reduces packet loss |
| Connection migration | QUIC native | Seamless IP/port changes (mobile support) |

---

## REST API & Desktop UI

### Local HTTP API (spawned by `thru ui`)

| Endpoint | Method | Purpose |
|----------|--------|---------|
| `/host` | POST | Start sender session (paths, server URL, STUN/TURN config, QUIC params) |
| `/receive` | POST | Start receiver session (join code, output dir, config) |
| `/abort` | POST | Stop ongoing transfer |
| `/abortReceiver` | POST | Stop specific receiver (sender-side) |
| `/events` | GET | Server-Sent Events stream (progress, status, errors) |

### SSE Event Types

**Common:** `connecting`, `connect_success`, `connect_error`, `progress`
**Sender:** `join_code_issued`, `manifest_build_start`, `manifest_build_progress`, `manifest_sealed`
**Receiver:** `p2p_start`, `p2p_success`, `p2p_failed`, `quic_handshake_success`, `manifest_receive_progress`, `manifest_unsealed`, `receive_complete`

---

## Security Considerations

### Strengths

- End-to-end encryption via QUIC TLS 1.3 (not optional)
- Server blind to file content (signaling only)
- AEAD authentication on every QUIC frame (tamper detection)
- High-entropy join codes (~95 bits, brute-force infeasible)
- Perfect Forward Secrecy (QUIC session key rotation)
- Open source (MIT license, auditable)

### Limitations

| Limitation | Detail |
|------------|--------|
| Join code reused for 24h | Session timeout means code is valid longer than needed |
| No identity verification beyond join code | No PKI, no signing, no PAKE |
| UDP-only | Won't work on UDP-blocking networks |
| TURNS not supported | TLS-over-TURN not available yet |
| No audit trail | No server-side transfer logging |
| Clock skew | Resume logic assumes consistent system time |

---

## Relevance to Wani

### Patterns to Adopt

| Pattern | Why |
|---------|-----|
| **ICE-based NAT traversal (libnice)** | Proven, handles all NAT types, automatic TURN fallback — exactly what wani wants |
| **QUIC transport** | Built-in encryption, multiplexing, congestion control; dramatically faster for multi-file |
| **Manifest-first file transfer** | Resume support, pre-validation, accurate progress; elegant design |
| **REST API + SSE for UI** | Clean decoupling of transfer engine from frontend; enables CLI + GUI + web |
| **Stateless signaling server** | Simple, scalable, privacy-preserving; good model for wani-server |
| **Auto-provisioned TURN credentials** | Server generates time-limited creds; no client-side config needed |
| **Multi-receiver support** | Useful for wani-pond scenarios (sharing to group) |
| **Rate limiting on signaling** | Practical abuse prevention for public servers |

### Patterns to Avoid or Modify

| Pattern | Why |
|---------|-----|
| **No PAKE/SPAKE2** | Wani specifies key exchange for identity; join code as session-proof alone is weaker than cryptographic key exchange |
| **UDP-only (no TCP fallback)** | Some enterprise networks block UDP; wani should have TCP relay as last resort |
| **24-hour session timeout** | Too long for ephemeral transfers; shorter default with explicit extension for ponds |
| **C++ codebase** | High complexity, memory safety concerns; wani may prefer Go or Rust for faster development and safety |
| **No file compression** | For wani, optional compression could help on slow connections (user's choice) |

### Open Questions for Wani

1. Should wani use QUIC for both P2P and relay, or QUIC for P2P and TCP for relay (broader compatibility)?
2. Thruflux uses QUIC's built-in TLS. Should wani add PAKE/SPAKE2 **on top** of QUIC TLS for identity verification, or is QUIC TLS + high-entropy join code sufficient?
3. Thruflux's multi-receiver model (up to 10) is interesting for wani-pond. How should group transfers work in a pond context?
4. libnice is C; if wani is written in Go/Rust, what are the ICE library options? (e.g., Go: `pion/ice`; Rust: `str0m`, `libnice-rs`)
5. Thruflux benchmarks show TURN relay adds ~144% overhead. Is this acceptable for wani's relay fallback, or should wani optimize its relay differently?
