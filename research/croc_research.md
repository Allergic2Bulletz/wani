# Croc — Research Document

> **Source:** [GitHub](https://github.com/schollz/croc) | [Developer Blog](https://infinitedigits.co/croc/) | Developer interviews
> **Author:** Zack Schollz (@schollz)
> **License:** MIT

---

## Overview & Philosophy

Croc is a CLI file transfer tool written in Go. Its core design philosophy prioritizes **simplicity and reliability over peer-to-peer optimization**. It intentionally uses a relay server instead of NAT hole punching, trading some speed for 100% compatibility with any network configuration.

Key design goals (from developer blog):
- File transfers should be **as fast as possible**
- File transfers should be **easy** — no port forwarding, no server setup, no accounts
- File transfers should be **secure** — end-to-end encryption with no trust in the relay

---

## Tech Stack

| Component | Detail |
|-----------|--------|
| **Language** | Go 1.22+ (89.9% of codebase) |
| **Distribution** | Single static binary; cross-platform (Windows, Linux, macOS, FreeBSD, Termux/Android, Docker) |
| **Build** | Standard Go toolchain; shell scripts (9.7%) for CI/release |

### Core Dependencies (from go.mod)

| Dependency | Purpose |
|------------|---------|
| `github.com/schollz/pake/v3` | Custom PAKE implementation (password-authenticated key exchange) |
| `github.com/schollz/peerdiscovery` | Local network peer discovery via multicast |
| `github.com/schollz/progressbar/v3` | Terminal progress visualization |
| `golang.org/x/crypto` | Standard crypto operations |
| `golang.org/x/net` | Networking utilities |
| `golang.org/x/time/rate` | Rate limiting |
| `github.com/cespare/xxhash/v2` | Fast file hashing (default) |
| `github.com/kalafut/imohash` | Alternative fast hashing for large files (>10MB) |
| `github.com/minio/highwayhash` | HighwayHash support |
| `github.com/magisterquis/connectproxy` | SOCKS5 proxy support (Tor) |
| `github.com/skip2/go-qrcode` | QR code generation for code sharing |
| `github.com/denisbrodbeck/machineid` | Machine identification |
| `github.com/sabhiram/go-gitignore` | .gitignore parsing for file exclusions |
| `github.com/schollz/cli/v2` | CLI framework |
| `filippo.io/edwards25519` | Edwards25519 elliptic curve (used by PAKE) |
| `github.com/tscholl2/siec` | Novel SIEC elliptic curve (default curve for PAKE) |

---

## End-to-End Process Flow

### Step 1: Code Phrase Generation

```
Sender runs: croc send [files]
  → Generates random human-pronounceable code phrase (6+ chars from word list)
  → Displays code phrase to user (+ copies to clipboard)
  → Derives room name: SHA256(first_4_chars + "croc")
```

### Step 2: Local Network Discovery (Parallel)

```
Sender broadcasts on local network via multicast (peerdiscovery library)
  → Payload: "croc" + relay_port
  → Duration: 30 seconds
  → If receiver found locally → use direct local connection (skip relay)
```

### Step 3: Relay Connection

```
Both parties connect to relay server on port 9009 (TCP)
  → Identify via room name (derived from code phrase)
  → Relay delivers messages between sender and receiver
```

### Step 4: PAKE Key Exchange

```
Sender:  pake.InitCurve(secret[5:], role=0, curve) → sends A.Bytes() as "pake1"
Receiver: pake.InitCurve(secret[5:], role=1, curve) → sends B.Bytes() as "pake2"
  → Both call .Update() with received bytes
  → Salt (8 random bytes) generated and exchanged
  → Both call .SessionKey() → identical strong symmetric key
  → If eavesdropped: all parties derive DIFFERENT keys → detection occurs
```

### Step 5: Encrypted File Transfer

```
Additional relay ports (9010-9013) used for parallel data streams
  → Default: 16 parallel TCP channels
  → Data encrypted with PAKE-derived session key
  → Files chunked for resumability
  → Hash verification per chunk
```

### Step 6: Completion

```
  → Receiver verifies file integrity (xxHash or imohash)
  → Transfer complete; ephemeral room destroyed
```

### Flow Diagram

```
Sender                    Relay Server (9009-9013)              Receiver
  |                              |                                |
  |--- connect to room -------->|                                |
  |                              |<------- connect to room ------|
  |--- pake1 (A.Bytes) -------->|-------- pake1 forwarded ----->|
  |                              |<------- pake2 (B.Bytes) ------|
  |<-- pake2 forwarded ---------|                                |
  |                              |                                |
  | [Both derive session key]    |   [Both derive session key]   |
  |                              |                                |
  |=== encrypted file data ====>|======= relay to receiver ====>|
  |    (ports 9010-9013)         |       (16 parallel TCP)       |
  |                              |                                |
  |--- transfer complete ------>|<------ ack -------------------|
```

---

## NAT Traversal Strategy

### Design Choice: Relay-Based (No Hole Punching)

Croc **intentionally avoids** NAT hole punching. From the developer blog:

> "File transfers can be easier by eliminating the need for hosting a server or port forwarding. Again, using a relay server allows any two computers to connect to one another without resorting to port forwarding or fiddling with a server."

**Rationale:** 100% compatibility with any network — firewalls, NATs, corporate networks — without user configuration.

### Local Network Optimization

Before using the remote relay, croc tries local connections:

1. Sender broadcasts presence via multicast using `peerdiscovery` library
2. Receiver also performs peer discovery
3. If both are on the same LAN → direct local connection (milliseconds vs ~200ms relay)
4. Falls back to relay if local attempt fails within ~100ms timeout

### Proxy Support

- SOCKS5 proxy support: `croc --socks5 "127.0.0.1:9050" send file` (works with Tor)
- Works from behind restrictive firewalls that allow outbound TCP

---

## Encryption & Key Exchange

### PAKE (Password-Authenticated Key Exchange)

From the developer blog:

> "Weak passwords can be used to make strong passwords using PAKE. PAKE is a cryptographic method where two people share a password which is then used — via back-and-forth communication — to generate a strong key. Since the two people generate the strong key by exchanging information, no one else could possibly learn the strong key even if they have the original password."

**Algorithm basis:** Dan Boneh & Victor Shoup's PAKE2 protocol (Stanford Cryptography textbook, pg 789)

### Elliptic Curve Options

| Curve | Description | Notes |
|-------|-------------|-------|
| **SIEC** (default) | Novel curve by @tscholl2 (author's brother, cryptographer) | Designed specifically for PAKE; fast arithmetic |
| P-256 | NIST standard (secp256r1) | 256-bit security |
| P-384 | NIST standard (secp384r1) | 384-bit security |
| P-521 | NIST standard (secp521r1) | 521-bit, highest security option |

Selected via `--curve` flag. Curves are hard-coded to prevent backdoors from user-supplied points.

### Security Properties

- **Eavesdropper resistance:** If anyone listens to the PAKE exchange, all parties derive different keys → detection
- **Single-use codes:** Code phrase only used for key exchange, never reused
- **Server transparency:** Relay cannot decrypt data (only sees ciphertext)
- **Symmetric encryption:** Data encrypted with PAKE-derived session key after exchange

### File Integrity

| Algorithm | Use Case |
|-----------|----------|
| xxHash (default) | Fast hashing for most files |
| imohash | Optimized for large files (>10MB); selectable via `--hash imohash` |

---

## Relay Server Design

### Default Public Relay

| Property | Value |
|----------|-------|
| **Host** | getcroc.schollz.com |
| **Ports** | 9009-9013 (TCP) |
| **Funding** | Author personally pays for bandwidth |
| **Storage** | None — relay only, no persistence |

### Architecture

- **Stateless:** No database or disk required
- **Ephemeral rooms:** Exist only during transfer; destroyed on completion
- **Multi-port:** Ports 9010-9013 allow parallel data streams (multiplexing)
- **Full-duplex:** Data flows simultaneously in both directions
- **Password protection:** Optional for self-hosted instances (`--pass` flag)

### Self-Hosting

```bash
# Minimal
croc relay

# Custom ports
croc relay --ports 1111,1112

# Docker with password
docker run -d -p 9009-9013:9009-9013 -e CROC_PASS='YOURPASSWORD' docker.io/schollz/croc
```

Clients connect to custom relay:
```bash
croc --relay "myrelay.example.com:9009" send file.txt
```

---

## Code Sharing / Pairing Mechanism

### Code Phrase Format

| Property | Detail |
|----------|--------|
| **Minimum length** | 6 characters |
| **Generation** | Random from phonetically distinct word list |
| **Purpose** | Human-pronounceable for verbal/manual transmission |
| **Single-use** | Never reusable after transfer |

### Room Derivation

```
room_name = SHA256(first_4_chars_of_code + "croc")
PAKE input = code[5:]  (characters 5+ of the code phrase)
```

### Sharing Methods

| Method | Command |
|--------|---------|
| Terminal display + clipboard | `croc send file` (default) |
| QR code | `croc send --qr file` |
| Environment variable | `CROC_SECRET="code" croc` (required on Linux/macOS to avoid CVE-2023-43621 process name leakage) |
| Extended clipboard | `croc --extended-clipboard send file` |

### Authentication Flow

```
1. Sender displays: "Code is: [6-word phrase]"
2. Receiver types: croc [6-word phrase]
3. Both derive room ID: SHA256(first_4_chars + "croc")
4. Connect to same relay room
5. PAKE exchange occurs automatically
6. Transfer begins if keys match
```

---

## Design Decisions & Trade-offs

### 1. Relay Over Hole Punching

> "File transfers can be easier by eliminating the need for hosting a server or port forwarding."

- **Pro:** 100% NAT/firewall compatibility without user configuration
- **Con:** All data flows through relay (bandwidth cost, latency)
- **Mitigation:** Local network optimization via multicast discovery

### 2. PAKE Over Simple Passwords

> "Passwords are easy, but passwords can be weak. It turns out many people use the same passwords so that breaking an encryption can be as easy as just trying each password in a list of pwned accounts."

- **Pro:** Single-use weak passwords generate strong session keys; no lookup attacks
- **Con:** Slightly more complex protocol than pre-shared keys

### 3. Go Over Python (vs. magic-wormhole)

> "For me the main reason was that I find installing Python on a Windows computer is troublesome, especially for people that don't touch a terminal. I like croc because it is an executable that you can receive a file just by double-clicking it and following the prompt."

- **Pro:** Single static binary; no runtime dependencies; non-technical users on Windows
- **Con:** Less flexible than Python ecosystem for rapid protocol iteration

### 4. Transfer Resumption (vs. magic-wormhole)

> "I decided not to follow the magic-wormhole spec because support for resuming transfers was an issue."

- **Pro:** Interrupted transfers can resume from last completed chunk
- **Implementation:** Chunk tracking via hash verification; distinguishes overwrite vs resume

### 5. IPv6-First with IPv4 Fallback

- **Pro:** Future-proof; better performance on IPv6 networks
- **Con:** Some networks have broken IPv6; fallback adds latency

### 6. Security Vulnerability Response

- SPAKE2 vulnerability found by RedRocket security team (2022)
- Fixed within 1 week with cryptographer assistance
- Server temporarily shut down during fix as precaution
- Demonstrates mature security practices

---

## Performance Characteristics

### Speed Design

> "File transfers should be as fast as possible — the time it takes to transfer the file is often spent waiting."

**Relay vs traditional upload-then-download:**
- Traditional: effective speed = ½ × harmonic mean of upload/download
- Croc relay: speed limited by slower of upload or download (simultaneous streaming)
- Example: 5 Mbps up + 8 Mbps down → traditional ≈ 3.1 Mbps, relay ≈ 5-8 Mbps

### Configuration

| Feature | Default | Flag |
|---------|---------|------|
| Parallel TCP channels | 16 | `--no-multiplexing` to disable |
| Rate limiting | Unlimited | `--throttle` with units (k, m, g) |
| Compression | Enabled | `--no-compress` to disable |
| IPv6 preference | IPv6-first | Falls back to IPv4 automatically |

### File Transfer Features

- Single or multiple files/folders
- Folder compression to ZIP
- Resume from interruption
- Empty folder preservation
- Symlink support
- File permission/timestamp preservation
- .gitignore support for exclusions
- stdin/stdout piping
- Text-only transfer mode

### Benchmark Context (from Thruflux benchmarks)

| Scenario | Croc | For comparison |
|----------|------|----------------|
| 10 GB single file | 2m 40s | Thruflux P2P: 2m 20s |
| 10k small files | 19m 33s | Thruflux P2P: 2m 18s |

Croc is competitive on large single files but significantly slower on many small files (TCP per-file overhead).

---

## Vulnerability History

| Date | Issue | Resolution |
|------|-------|------------|
| 2022 | SPAKE2 point validation vulnerability (RedRocket team) | Fixed in 1 week; server shut down during fix |
| 2023 | CVE-2023-43621: code phrase leaked via process name on Linux/macOS | Changed default to require `CROC_SECRET` env var; `--classic` flag for opt-in legacy |

---

## Relevance to Wani

### Patterns to Adopt

| Pattern | Why |
|---------|-----|
| **PAKE for code-based pairing** | Human-shareable codes → strong session keys; proven approach; fits wani's "key exchange for identity" |
| **Human-pronounceable codes** | Good UX for verbal/manual code sharing |
| **Single binary distribution** | Plug-and-play requirement; no runtime deps |
| **Transfer resumption** | Essential for large files over unreliable connections |
| **Self-hostable relay** | Needed for wani-pond; croc shows how simple this can be |
| **Local network optimization** | Multicast peer discovery for LAN transfers is a low-cost win |

### Patterns to Avoid or Modify

| Pattern | Why |
|---------|-----|
| **Relay-only architecture** | Wani specifically wants direct P2P via hole punching; relay as fallback only |
| **TCP-only transport** | TCP has high overhead for many small files (19m vs 2m in benchmarks); consider QUIC |
| **SHA256-based room derivation from code prefix** | Leaks partial code info to relay; consider fully hashed room IDs |
| **Custom SIEC curve as default** | Less audited than NIST curves or Curve25519; consider standardized curves unless performance demands it |

### Open Questions for Wani

1. Should wani use PAKE (like croc) or SPAKE2 (like wormhole) for the key exchange? SPAKE2 has an RFC (9382); PAKE is more general.
2. Croc's relay is extremely simple (TCP pipe). If wani uses QUIC for P2P, should the relay also speak QUIC or use TCP as a simpler fallback?
3. Croc's code phrase uses only 6 chars minimum. Is this sufficient entropy for wani's threat model, or should wani require higher entropy?
