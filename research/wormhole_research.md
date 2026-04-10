# Magic-Wormhole — Research Document

> **Source:** [GitHub](https://github.com/magic-wormhole/magic-wormhole) | [ReadTheDocs](https://magic-wormhole.readthedocs.io/) | Protocol documentation
> **Author:** Brian Warner
> **License:** MIT

---

## Overview & Philosophy

Magic-wormhole is a Python-based file transfer tool with a clean, protocol-first design. It pioneered the concept of human-transcribable code phrases for establishing encrypted connections between strangers. The project is designed as a **protocol** first and a tool second — multiple reimplementations exist in Go, Rust, and JavaScript.

Key design philosophy:
- Get things from one computer to another, **safely**
- Use short human-pronounceable codes (wormhole codes) as the only shared secret
- No accounts, no pre-shared keys, no PKI — just a code phrase exchanged out-of-band
- The protocol is the product; the CLI is just one implementation

---

## Tech Stack

| Component | Detail |
|-----------|--------|
| **Language** | Python 3.10+ (tested through 3.13) |
| **Async Framework** | Twisted (Deferred-based async) |
| **Distribution** | PyPI package (`magic-wormhole`) |
| **Alternative Implementations** | Go (`wormhole-william`), Rust (`wormhole-rs`), JavaScript (partial) |

### Core Dependencies

| Dependency | Purpose |
|------------|---------|
| `PyNaCl` (≥1.2.0) | Cryptographic primitives — Curve25519, NaCl SecretBox (libsodium bindings) |
| `spake2` (≥0.8) | SPAKE2 key exchange (RFC 9382 compliant) |
| `Twisted` (≥17.10.0) | Async networking, WebSocket support |
| `autobahn` (≥17.10.0) | WebSocket client/server (Mailbox protocol) |
| `click` | CLI argument parsing |
| `attrs` | Class definitions |
| `zfspy` | Filesystem traversal for directory transfers |
| `incremental` | Version management |

### Cryptographic Stack

| Layer | Algorithm | Library |
|-------|-----------|---------|
| Key exchange | SPAKE2 (RFC 9382) | `spake2` |
| Key derivation | HKDF-SHA256 | `nacl` |
| Symmetric encryption (mailbox) | NaCl SecretBox (XChaCha20-Poly1305) | `PyNaCl` |
| Symmetric encryption (dilation) | Noise `NNpsk0_25519_ChaChaPoly_BLAKE2s` | Custom |
| Hashing | SHA256 | Standard library |

---

## End-to-End Process Flow

### Architecture Layers

Magic-wormhole has a cleanly layered architecture:

```
Layer 4: Application (file transfer offer/answer)
Layer 3: Transit (bulk encrypted TCP data transfer)
Layer 2: Encrypted Mailbox Messages (SPAKE2 + NaCl SecretBox)
Layer 1: Mailbox Protocol (WebSocket JSON messages)
Layer 0: Network (WebSocket to Mailbox Server, TCP to Transit Relay)
```

### Detailed Flow

#### Phase 1: Setup & Nameplate Allocation

```
Sender                          Mailbox Server
  |                                   |
  |-- BIND (appid, side) ----------->|
  |<-- WELCOME (motd, version) ------|
  |-- ALLOCATE {} ------------------>|
  |<-- ALLOCATED {nameplate: "42"} --|
  |-- CLAIM {nameplate: "42"} ------>|
  |<-- CLAIMED {mailbox: random-id} -|
  |-- OPEN {mailbox: random-id} ---->|
```

#### Phase 2: Receiver Joins

```
Receiver                        Mailbox Server
  |                                   |
  |-- BIND (appid, side) ----------->|
  |<-- WELCOME -----------------------|
  |-- CLAIM {nameplate: "42"} ------>|
  |<-- CLAIMED {mailbox: random-id} -|
  |-- OPEN {mailbox: random-id} ---->|
```

#### Phase 3: SPAKE2 Key Exchange (via Mailbox)

```
Sender                          Mailbox Server                    Receiver
  |                                   |                               |
  |-- ADD {phase:"pake_v1", body:X+M*pw} -->|                        |
  |                                   |-- MESSAGE {pake_v1} -------->|
  |                                   |<-- ADD {phase:"pake_v1", body:Y+N*pw} --|
  |<-- MESSAGE {pake_v1} ------------|                               |
  |                                   |                               |
  | [Both derive shared secret K]     |    [Both derive shared secret K]
```

#### Phase 4: Encrypted Version Exchange

```
  |-- ADD {phase:"version", encrypted} -->|-- MESSAGE -------->|
  |<-- MESSAGE {version, encrypted} ------|<-- ADD ------------|
  |                                       |                     |
  | [Verifier confirmed; capabilities exchanged]               |
```

#### Phase 5: Transit Negotiation

```
  |-- ADD {transit hints, encrypted} ---->|-- MESSAGE -------->|
  |<-- MESSAGE {transit hints, encrypted} |<-- ADD ------------|
  |                                       |                     |
  | [Both now have each other's IP:port hints + relay address] |
```

#### Phase 6: Direct or Relay Connection

```
  Sender                                                    Receiver
    |                                                          |
    |<---- attempt direct TCP to each other's hints --------->|
    |                                                          |
    |---- if direct fails: both connect to Transit Relay ----->|
    |                                                          |
    | Handshake: "transit sender <HKDF-token> ready\n\n"      |
    |            "transit receiver <HKDF-token> ready\n\n"     |
    |                                                          |
    | [Sender picks winning connection if multiple succeed]    |
```

#### Phase 7: File Transfer

```
  |-- offer message (file metadata) ----- [via mailbox, encrypted] ----->|
  |<-- answer message (acceptance) ------- [via mailbox, encrypted] -----|
  |                                                                       |
  |===== binary file data ============== [via transit, encrypted] ======>|
  |                                                                       |
  |<---- SHA256 ack -------------------- [via transit, encrypted] -------|
```

#### Phase 8: Close

```
  |-- ADD {phase:"close", mood:"happy"} -->|-- MESSAGE -------->|
  |<-- MESSAGE {close, mood:"happy"} ------|<-- ADD ------------|
```

---

## NAT Traversal Strategy

### Multi-Strategy Parallel Approach

Magic-wormhole does **not** use STUN/TURN/ICE. Instead:

1. **Direct TCP (preferred)**
   - Both sides detect local IP addresses via network interfaces
   - Listen on a random TCP port
   - Exchange addresses as encrypted "hints" via mailbox
   - Peers attempt simultaneous connections to each other's public/LAN IPs

2. **Transit Relay (fallback)**
   - If direct connections fail, both connect to centralized Transit Relay
   - Relay protocol: `please relay <HKDF-derived-token>\n` → `ok\n` → bidirectional pipe
   - Relay is a transparent encrypted pipe — cannot decrypt data

3. **Tor onion service (optional)**
   - `tor-tcp-v1` hint type for .onion addresses
   - ~30 second setup overhead for onion service
   - Full anonymity at cost of latency

### Connection Hints

| Hint Type | Description |
|-----------|-------------|
| `direct-tcp-v1` | Standard TCP (hostname:port:priority) |
| `relay-v1` | Transit relay endpoint |
| `tor-tcp-v1` | Tor onion service (v3 addresses) |

### Connection Race

- Multiple connections may succeed simultaneously
- **Sender role** decides which connection to keep
- Loser connections terminated cleanly
- Priority scores in hints help select best option

### Keepalives

- WebSocket keepalives: 60 sec (Autobahn)
- Dilation L3 keepalives: 30-60 sec ping/pong
- Prevents NAT timeout for long-idle connections

### Key Difference from STUN/TURN

- No external STUN server needed (clients discover own IPs)
- No complex ICE candidate management
- Single simple relay vs multiple TURN servers
- Simpler but less reliable than ICE for symmetric NAT

---

## Encryption & Key Exchange

### SPAKE2 (RFC 9382)

**Standard:** IETF RFC 9382 (Simplified Password Authenticated Key Exchange)

#### Process

```
Shared input: pw = wormhole code (16-256 bits)

Sender:
  x = random 256-bit scalar
  msg1 = x·G + M·pw        [sent unencrypted via mailbox]

Receiver:
  y = random 256-bit scalar
  msg2 = y·G + N·pw        [sent unencrypted via mailbox]

Both derive:
  K = hash(X, Y, msg1, msg2, shared_secret)
  verifier = SHA256(K)      [optional manual comparison]
```

#### Key Derivation (HKDF-SHA256)

All symmetric keys derived from shared secret K:

| Key | Derivation |
|-----|-----------|
| Per-phase mailbox key | `HKDF(K, info="wormhole:phase:" + SHA256(side) + SHA256(phase))` |
| Transit key | `HKDF(K, info="transit_key")` |
| Dilation key | `HKDF(K, info="dilation-v1")` |

#### Message Encryption (NaCl SecretBox)

```
plaintext → SecretBox(key=per_phase_key)
  → random 24-byte nonce
  → XChaCha20-Poly1305 encryption
  → 32-byte authentication tag
  → output: nonce ‖ ciphertext ‖ tag (44+ bytes overhead)
  → sent as hex string in mailbox message body
```

### Threat Model

| Attacker | Capability | Risk |
|----------|-----------|------|
| Passive network observer | Sees IPs, timing | Cannot decrypt (SPAKE2 + SecretBox) |
| Active mailbox server | Intercepts PAKE messages | 1-in-65536 chance of guessing 16-bit code per attempt |
| Compromised transit relay | Sees encrypted bytes | Cannot decrypt (encrypted with SPAKE2-derived key) |
| Malicious peer | Multiple claims | Detected via "crowded" mood |

**Attack cost:** Attacker gets ONE guess per code use (mailbox prevents reuse). At default 16-bit entropy, 1-in-65536 per attempt.

---

## Mailbox Server Design

*Separate repository: `magic-wormhole-mailbox-server`*

### Architecture

| Property | Detail |
|----------|--------|
| **Protocol** | WebSocket (Autobahn) |
| **Storage** | SQLite database for message persistence |
| **Default** | `relay.magic-wormhole.io` (best-effort uptime) |
| **Statefulness** | Stateless logic; all state in DB; survives restart |

### Responsibilities

1. **Nameplate allocation** — Short numeric IDs (1-100+ range) mapped to random 128-bit mailbox identifiers
2. **Message routing** — Store and forward JSON messages; no decryption; queue for offline clients
3. **Application isolation** — AppID scopes all resources; prevents cross-app leakage
4. **Mood tracking** — Records connection outcomes: happy/lonely/scary/errory/pruney/crowded

### Nameplate Lifecycle

```
ALLOCATE → server assigns lowest available numeric ID
CLAIM    → both sides claim same nameplate → get mailbox ID
OPEN     → subscribe to mailbox messages
ADD      → send message to mailbox (phase + body)
MESSAGE  → receive message from mailbox
RELEASE  → release nameplate (enables reuse)
CLOSE    → disconnect with mood (happy/lonely/scary/errory)
```

### Database Schema (Simplified)

```
Nameplates:
  - id (integer, human-typed)
  - appid (string)
  - mailbox_id (128-bit random)
  - claimed_sides (set)
  - allocated_time (timestamp)

Mailboxes:
  - id (128-bit random)
  - appid (string)
  - messages [{phase, side, body, timestamp}]
  - open_sides (set)
  - moods {side → mood}
```

### Scalability

- Per-mailbox lifetime: typically < 5 minutes
- Per-connection memory: ~50KB (WebSocket state)
- SQLite sufficient for moderate scale; PostgreSQL for large deployments
- Stateless logic enables horizontal scaling behind load balancer

---

## Transit Relay Design

*Separate repository: `magic-wormhole-transit-relay`*

### Architecture

Extremely simple — just a TCP socket gluer.

```
Client A connects → sends: "please relay TOKEN_A\n"
  Relay stores TOKEN_A, waits for match
Client B connects → sends: "please relay TOKEN_B\n" (same TOKEN)
  Relay sends "ok\n" to both
  Relay pipes all subsequent bytes: A ↔ B bidirectionally
  Either client closes → relay closes both
```

### Properties

| Property | Detail |
|----------|--------|
| **Encryption** | Cannot sniff (end-to-end encrypted above relay) |
| **State** | Minimal — just active socket pairs |
| **Persistence** | None needed |
| **Scaling** | Stateless; horizontally scalable |
| **Bandwidth** | 2x relay bandwidth for full-duplex (all bytes transit relay) |
| **CPU** | Minimal — just socket I/O forwarding |

---

## Code Sharing / Pairing Mechanism

### Wormhole Code Format

```
Format: <nameplate>-<word1>-<word2>

Example: 7-crossover-clockwork

Components:
  7           → nameplate (allocated by server, 1-3+ digits)
  crossover   → random word from PGP wordlist (~2048 words, ~11 bits each)
  clockwork   → another random word
```

### Entropy

| Configuration | Entropy |
|--------------|---------|
| Default (2 words) | ~16 bits total (by convention) |
| `--code-length=3` | ~27 bits |
| `--code-length=4` | ~38 bits |
| Custom codes | Unlimited (any string with nameplate + words) |

### Code Generation

```python
# Sender
w = wormhole.create(appid, relay_url, reactor)
w.allocate_code(length=2)
code = await w.get_code()  # "7-crossover-clockwork"

# Receiver (with tab-completion)
w = wormhole.create(appid, relay_url, reactor)
h = w.input_code()
h.choose_nameplate("7")
h.choose_words("crossover-clockwork")
```

### Properties

- **Single-use:** Each code works exactly once
- **Tab-completion:** Receiver can tab-complete nameplates and words
- **Offline codes:** Both sides can use `w.set_code()` with pre-agreed codes (dice rolls, etc.)
- **Nameplate reuse:** Nameplates recycled after release; different apps/codes don't leak info

---

## Dilation Protocol (Optional Advanced Feature)

Dilation upgrades a basic mailbox-only wormhole to a persistent, multiplexed TCP connection.

### When Used

- Basic wormhole: short message exchange (offer/answer) via mailbox
- Dilated wormhole: persistent bidirectional streams (e.g., continuous file transfer, tunneling)

### Architecture

```
L1: Mailbox (WebSocket JSON) — used for setup only
L2: Dilation (Noise NNpsk0 encrypted TCP) — persistent connection
L3: Subchannels — multiplexed logical streams over L2
```

### Noise Protocol

- Pattern: `Noise_NNpsk0_25519_ChaChaPoly_BLAKE2s`
- Pre-shared key: dilation_key derived from SPAKE2 shared secret
- Ephemeral keys: different per connection → **Perfect Forward Secrecy**

### Leader/Follower

- Lexicographically higher "side" string = Leader
- Leader controls connection selection and keepalives
- Prevents split-brain scenarios

---

## Design Decisions & Trade-offs

### 1. PAKE Over Public Key Exchange

- **Chosen:** SPAKE2 (password-based, RFC standardized)
- **Rejected:** RSA/ECDH (requires PKI bootstrap)
- **Rationale:** Humans can transcribe 16-bit codes; no key management infrastructure needed

### 2. Separate Mailbox + Transit Servers

- **Chosen:** Mailbox for signaling (small messages, persistent); Transit for bulk data (high bandwidth, ephemeral)
- **Rejected:** Single server for everything
- **Rationale:** Mailbox stores messages in DB (expensive at scale); Transit is stateless pipe (cheap)
- **Trade-off:** Two servers to deploy and maintain

### 3. Direct TCP + Relay Fallback

- **Chosen:** Race direct connections against relay; sender picks winner
- **Rejected:** Always relay (simpler) or always direct (unreliable)
- **Rationale:** Optimize common case (same LAN) while ensuring fallback

### 4. No TLS Between Client and Server

- **Chosen:** End-to-end encryption at app layer; mailbox/relay see ciphertext
- **Rejected:** TLS between client-server
- **Rationale:** Server is not trusted; encryption is above transport layer
- **Trade-off:** Plaintext metadata visible (side/phase info)

### 5. Protocol-First Design

- **Chosen:** Well-specified protocol enabling multi-language implementations
- **Rejected:** Single-implementation CLI tool
- **Rationale:** Ecosystem of compatible clients (Go, Rust, JS, GUI apps)
- **Result:** Warp (GNOME GUI), wormhole-william (Go), Fowl (high-perf), Git With Me

### 6. Low Default Entropy (16 bits)

- **Chosen:** Short codes for usability (2 words)
- **Rejected:** Long codes for security
- **Rationale:** Attacker gets only 1 guess per code use; at 16 bits, 1-in-65536 chance
- **Trade-off:** Vulnerable if attacker controls mailbox server AND intercepts network

---

## Ecosystem & Extensions

### Multi-Language Implementations

| Implementation | Language | Status |
|---------------|----------|--------|
| magic-wormhole | Python | Reference (most complete) |
| wormhole-william | Go | Active |
| wormhole-rs | Rust | Active |
| Browser implementations | JavaScript/WebRTC | Partial |

### Apps Built on Wormhole Protocol

| App | Purpose |
|-----|---------|
| Warp | GNOME desktop GUI |
| Fowl | High-performance file transfer via Dilation |
| Git With Me | Git clone over wormhole |
| Pear-On | tty-share over wormhole |

---

## Relevance to Wani

### Patterns to Adopt

| Pattern | Why |
|---------|-----|
| **SPAKE2 (RFC 9382)** | Standardized, audited, well-understood; preferred over croc's custom PAKE |
| **Layered protocol architecture** | Clean separation of signaling, key exchange, and data transfer; easier to reason about and extend |
| **AppID-scoped nameplates** | If wani-pond supports multiple apps/contexts, appid isolation prevents cross-contamination |
| **Mood tracking** | Useful for server operators to diagnose issues and detect abuse |
| **Protocol-first design** | Enables future GUI clients, mobile apps, and third-party integrations without reimplementing crypto |
| **Phase-based message multiplexing** | Clean way to handle multiple message types over single channel |

### Patterns to Avoid or Modify

| Pattern | Why |
|---------|-----|
| **Python runtime requirement** | Installation friction; wani should be a single binary |
| **16-bit default entropy** | Too low if wani-server is untrusted; prefer higher entropy codes |
| **No STUN (client self-discovery only)** | Works but less reliable than ICE for symmetric NAT; wani wants proper hole punching |
| **Separate mailbox + transit servers** | Operational complexity; wani-server should combine signaling and relay |
| **SQLite for mailbox persistence** | Adds complexity; consider whether wani needs message persistence or can be fully ephemeral |
| **Twisted async framework** | Dated; if implementing in Python, prefer asyncio. But wani likely not Python. |

### Open Questions for Wani

1. Should wani adopt the full wormhole protocol spec, or design a simpler custom protocol? Wormhole's spec enables ecosystem, but adds complexity.
2. Wormhole's Dilation protocol provides PFS for persistent connections — relevant for wani-pond's long-lived group channels?
3. Wormhole's mailbox server persists messages for offline delivery. Does wani-server need this (for ponds), or can it be fully ephemeral?
4. The wormhole ecosystem has Go and Rust implementations. Could wani build on `wormhole-william` (Go) or `wormhole-rs` (Rust) instead of starting from scratch?
