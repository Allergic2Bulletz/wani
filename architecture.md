# Wani — Architecture Reference

> Decided architecture for implementation.

---

## 1. Language: Go

Single static binary. Key libraries: `quic-go`, `pion/ice`, `pion/turn`, `cespare/xxhash`.

## 2. P2P Transport: QUIC

Reliable, ordered, encrypted, multiplexed over UDP. TLS 1.3 mandatory. NAT-hole-punch friendly via UDP. Networks that block UDP entirely fall back to relay (Decision 4).

Library: `github.com/quic-go/quic-go`

## 3. NAT Traversal: Full ICE

STUN candidate gathering → UDP hole punching → TURN relay fallback → TCP relay last resort. ICE handles candidate prioritization automatically.

Library: `github.com/pion/ice/v4`

Public STUN servers used by default: `stun.cloudflare.com:3478`, `stun.l.google.com:19302`.

## 4. Relay Fallback: TURN (primary) + TCP relay (last resort)

- **TURN (UDP):** Standard ICE fallback. Client code is identical between direct P2P and TURN relay — QUIC runs over the relay transparently.
- **TCP WebSocket relay:** Last resort when UDP is blocked entirely. Data relayed through the signaling WebSocket connection. Lower performance (HTTP framing overhead). Log a warning when this path is used.

## 5. Signaling: WebSocket

Persistent bidirectional WebSocket connection for:
- Session creation and pairing code exchange
- SPAKE2 message relay (server forwards opaquely, cannot derive key)
- ICE candidate trickle relay (server forwards opaquely, does not parse SDP)

Works through corporate HTTP proxies.

## 6. Key Exchange: SPAKE2 (RFC 9382) + QUIC TLS — Option B

**Flow:**
1. Both peers connect to wani-server via WebSocket
2. Sender creates session → receives 4-word pairing code
3. Receiver joins session with pairing code
4. SPAKE2 exchange over WebSocket → both peers derive shared secret K
5. ICE candidate exchange over WebSocket → select best network path
6. QUIC connection established with normal TLS (no PSK mode)
7. First QUIC message: `HMAC-SHA256(K, "wani-quic-verify")` — proves peer completed same SPAKE2 exchange
8. File transfer proceeds over QUIC

**Key property:** Even if the signaling server is compromised, the attacker cannot derive K without knowing the pairing code. The server sees SPAKE2 messages but cannot extract the shared secret.

**Not double encryption:** SPAKE2 is used for identity verification only. QUIC TLS 1.3 (AES-GCM or ChaCha20-Poly1305 AEAD) handles all data encryption.

## 7. Pairing Codes: 4 Human-Pronounceable Words

Format: `word-word-word-word` (e.g., `blue-hammer-ocean-tiger`)
Entropy: ~33-44 bits (from ~2048-word curated list, 11 bits per word)
Selection: `crypto/rand`
Configurable: `--words N` flag (default 4)

Wordlist embedded via `//go:embed` in `internal/protocol/`.

**Threat model:** With an honest signaling server, attacker gets one guess per connection attempt — even 24 bits is safe. The 44-bit default hedges against a compromised server attempting offline SPAKE2 guessing.

## 8. File Transfer: Manifest-First

**Flow:**
1. Sender scans file tree → builds manifest: `[]FileEntry{Path, Size, XXHash, Compression}`
2. xxHash computed per file during scan
3. Manifest sent over QUIC control stream
4. Receiver parses manifest → creates directory structure → signals ready
5. Sender streams file data over QUIC
6. Receiver writes each file → computes xxHash → compares against manifest → marks `complete`

### 8a. Compression: None for MVP

Protocol reserves a `compression` field per file entry (enum: `none`, `zstd`). Sender sets `none` for all files. Receiver checks the field before writing. No implementation now; no breaking change needed to add later.

### 8b. Resume: Per-File for MVP

Manifest tracks `pending` / `complete` per file. Interrupted transfer restarts at next incomplete file. Partially transferred files are resent from the beginning.

**Future upgrade:** Per-chunk resume. Design chunk size as a configurable constant from the start so per-chunk can be layered on without protocol redesign.

### 8c. Integrity: QUIC AEAD + Per-File xxHash

Two layers serving different failure modes:
- **QUIC AEAD:** Catches in-transit bit-flips at the frame level (automatic, zero overhead)
- **Per-file xxHash:** Catches disk write errors, silent corruption, verifies complete transfer end-to-end

Library: `github.com/cespare/xxhash/v2` — `xxhash.Sum64`. ~10× faster than SHA256, negligible CPU overhead.

## 9. Server Architecture: Single Binary

wani-server = signaling + STUN + optional TURN relay.

- TURN enabled via `--relay` flag using `pion/turn` (`github.com/pion/turn/v4`)
- Per-session HMAC TURN credentials tied to SPAKE2 session, auto-expire when transfer ends
- Public STUN servers used by default (no self-hosted STUN needed)

**Migration path → coturn:** When relay load becomes a scaling concern or open-relay risk is unacceptable. ICE client code does not change; only server-side credential issuance and relay process change.

## 10. Client Architecture: Core Library + CLI

```
cmd/wani-client/       → CLI frontend: flags, terminal I/O, progress display
internal/client/       → Core transfer library: zero terminal I/O, clean Go API
internal/protocol/     → Shared types: messages, manifest, pairing codes
internal/identity/     → Identity interface + EphemeralIdentity
```

Core library is independently testable. CLI is a thin consumer. No `fmt.Println`, `os.Stdin`, or `log.Fatal` in `internal/`.

**Migration path → REST daemon:** When a GUI or browser frontend is actively being built, wrap the core library in a local HTTP server + SSE progress stream (Thruflux model). Core logic does not change.

## 11. Identity Model: Ephemeral MVP + Abstract Interface

MVP: `EphemeralIdentity` — keys derived from SPAKE2 shared secret, zero-lifetime, discarded after transfer. No key files, no contacts.

Interface in `internal/identity/`:
```go
type Identity interface {
    Sign(data []byte) []byte
    PublicKey() []byte
    Verify(data []byte, sig []byte) bool
}
```

All transfer logic uses the interface, never raw SPAKE2 key material.

**Migration path → ponds:** Add `PersistentIdentity` implementation (Ed25519 keypair loaded from disk, trust-on-first-use like SSH). No transfer logic changes.

## 12. Encryption Layering

Resolved by Decision 6 (Option B):
- **Identity layer:** SPAKE2 over WebSocket → HMAC proof inside first QUIC message
- **Transport layer:** QUIC TLS 1.3 (AES-GCM / ChaCha20-Poly1305 AEAD)

Not double encryption — SPAKE2 provides identity verification, QUIC TLS provides data encryption.

**Pond phase:** Consider Noise protocol for PFS on long-lived persistent connections if QUIC session resumption is insufficient.

## 13. Development & Deployment

- **VM:** DigitalOcean ($5/mo) or Oracle Cloud Free Tier. Public IP, open UDP ports + TCP/443.
- **Deploy:** GitHub Actions: build on push to `main` → SCP binary → SSH restart. systemd for auto-restart.
- **Daily dev:** Localhost loopback (both clients on same machine) for protocol/encryption/transfer logic.
- **NAT testing:** Cloud VM as wani-server + two devices on different networks (dev machine on WiFi + old laptop on phone hotspot).

---

## Quick Reference

| # | Decision | Choice |
|---|----------|--------|
| 1 | Language | Go |
| 2 | Transport | QUIC (`quic-go`) |
| 3 | NAT Traversal | Full ICE (`pion/ice`) |
| 4 | Relay | TURN (`pion/turn`) + TCP WebSocket fallback |
| 5 | Signaling | WebSocket |
| 6 | Key Exchange | SPAKE2 (RFC 9382) + QUIC TLS Option B |
| 7 | Pairing Codes | 4 words, ~33-44 bits |
| 8 | File Transfer | Manifest-first, no compression (extensible), per-file resume, AEAD + xxHash |
| 9 | Server | Single binary; `--relay` flag; coturn migration planned |
| 10 | Client | Core library + CLI; REST daemon migration planned |
| 11 | Identity | Ephemeral MVP; abstract `Identity` interface for ponds |
| 12 | Encryption | SPAKE2 identity + QUIC TLS transport (resolved by #6) |
| 13 | Dev/Deploy | Cloud VM + GitHub Actions CI/CD; localhost dev; two-device NAT testing |
