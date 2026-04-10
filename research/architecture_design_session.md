# Wani — Architecture Decisions

> This document enumerates the major architecture decisions for wani's MVP. Each decision includes the options, trade-offs, and relevant prior art. Decisions are roughly ordered from foundational (affects everything) to specific (affects one component).
>
> **Status legend:** `OPEN` = not yet decided | `LEANING` = preference noted but not committed | `DECIDED` = locked in

---

## Decision 1: Programming Language

`Status: DECIDED → Go`

The language choice affects everything: what libraries are available, how the binary is distributed, development speed, and performance.

| Option | Pros | Cons | Prior Art |
|--------|------|------|-----------|
| **Go** | Single static binary; excellent cross-compilation; strong stdlib for networking; goroutines for concurrency; fast to develop; `quic-go` and `pion/ice` libraries available | GC pauses (minor for file transfer); less control over memory; generics still maturing | Croc (Go); wormhole-william (Go reimplementation of wormhole) |
| **Rust** | No GC; memory safety; excellent performance; `quinn` (QUIC), `str0m` (ICE) libraries; single binary; strong crypto ecosystem | Steep learning curve; slower development; longer compile times | wormhole-rs (Rust reimplementation of wormhole) |
| **C++** | Maximum performance; mature libraries (lsquic, libnice); fine-grained control | Memory safety risks; complex build systems (CMake/vcpkg); slower development; harder cross-compilation | Thruflux (C++17) |
| **TypeScript/Node** | Fastest development; huge ecosystem; WebRTC built into Node; could share code with future web client | Not a single binary (needs Node runtime); worse performance; weaker crypto story | None of the three reference projects |

**Key considerations:**
- Wani's "plug-and-play" requirement strongly favors single-binary distribution (Go or Rust)
- This is a school project with a deadline — development speed matters
- Go has the most ergonomic QUIC + ICE library story (`quic-go` + `pion/ice` are both mature, pure-Go, and well-documented)
- Rust would be ideal for a production system but may be too slow to develop for a class project

---

## Decision 2: P2P Transport Protocol

`Status: DECIDED → QUIC`

How file data moves between peers once a direct connection is established.

| Option | Pros | Cons | Prior Art |
|--------|------|------|-----------|
| **QUIC** | Reliable + ordered + encrypted + multiplexed over UDP; NAT-hole-punch friendly; built-in congestion control; TLS 1.3 mandatory | UDP-only (some networks block UDP); newer protocol; library maturity varies by language | Thruflux |
| **TCP** | Universal support; reliable; simple | Can't traverse NAT holes reliably; no multiplexing; head-of-line blocking; separate TLS layer needed | Croc, Wormhole |
| **WebRTC Data Channels** | ICE + DTLS + SCTP bundled; browser-compatible | Designed for media, not bulk data; SDP complexity; SCTP message size limits; overkill | None of the three |
| **uTP** | Proven (BitTorrent); reliable over UDP; NAT-friendly | No encryption; no multiplexing; LEDBAT congestion control is deliberately slow; limited libraries | None of the three |
| **Raw UDP + custom reliability** | Maximum control | Reinventing QUIC poorly; massive engineering effort; security risks | None |

**Key considerations:**
- QUIC gives us reliable delivery, encryption, multiplexing, and NAT compatibility in one protocol
- The main risk is networks that block UDP entirely — this is the relay fallback case
- Thruflux benchmarks show QUIC excels at many-small-file transfers (2m 18s vs croc's 19m 33s for 10k files) due to stream multiplexing

---

## Decision 3: NAT Traversal Strategy

`Status: DECIDED → Full ICE`

How peers find and connect to each other through NATs and firewalls.

| Option | Pros | Cons | Prior Art |
|--------|------|------|-----------|
| **Full ICE (STUN + hole punch + TURN fallback)** | Systematic; handles all NAT types; prioritizes direct P2P; graceful fallback; RFC-standardized | More complex to implement; requires STUN service; TURN relay needed for symmetric NAT | Thruflux (via libnice) |
| **Relay-only** | 100% reliable; trivially simple; works everywhere | All data through relay; bandwidth cost; latency; single point of failure | Croc |
| **Direct TCP + relay fallback** | Simple direct attempt; no STUN needed | TCP hole punching unreliable (~60-80%); no systematic candidate gathering | Wormhole |
| **Custom UDP hole punch (no ICE)** | Simpler than full ICE; sufficient for most NATs | No TURN fallback; fails on symmetric NAT without relay; ad-hoc rather than standardized | None of the three |

**Key considerations:**
- Wani's core requirement is "direct P2P via hole punching, relay as fallback" — this is exactly what ICE does
- In Go: `pion/ice` is a mature, pure-Go ICE implementation (same team that builds `pion/webrtc`)
- In Rust: `str0m` or `libnice` bindings
- ICE handles the prioritization logic (try direct → try hole punch → fall back to relay) automatically

---

## Decision 4: Relay / Fallback Transport

`Status: DECIDED → TURN (primary) + TCP relay (fallback if UDP blocked)`

What protocol the relay server speaks when direct P2P fails.

| Option | Pros | Cons | Prior Art |
|--------|------|------|-----------|
| **TURN (UDP relay for QUIC)** | Standard ICE fallback; client code doesn't change between P2P and relay; QUIC runs identically over relay | UDP-only; TURN server is complex (coturn); bandwidth-heavy | Thruflux (coturn) |
| **TCP relay (custom)** | Works on networks that block UDP; simple to implement; lower server resource usage | Client needs separate TCP code path; no QUIC benefits on relay path; two transport implementations | Croc, Wormhole |
| **Both TURN + TCP relay** | Maximum compatibility: TURN for QUIC when UDP works, TCP relay when UDP is blocked entirely | Two relay implementations; more server complexity; more client code paths | None of the three |
| **WebSocket relay** | Works through HTTP proxies; firewall-friendly; signaling server can double as relay | Lower performance (HTTP framing overhead); not standard TURN | None of the three |

**Key considerations:**
- TURN-only (Thruflux model) means UDP-blocked networks get no service at all
- TCP relay (Croc/Wormhole model) means the relay path has no QUIC benefits
- "Both" gives maximum compatibility but more complexity
- A practical middle ground: use TURN as ICE's standard fallback, and if even UDP to the TURN server is blocked, fall back to a TCP WebSocket relay through the signaling connection (which already works since signaling uses WebSocket)

---

## Decision 5: Signaling Protocol

`Status: DECIDED → WebSocket`

How peers exchange connection metadata (ICE candidates, join codes, key exchange messages) before the P2P link is up.

| Option | Pros | Cons | Prior Art |
|--------|------|------|-----------|
| **WebSocket (persistent)** | Bidirectional; real-time; widely supported; works through HTTP proxies; server can push to clients | Persistent connection per client; slightly more server state | Thruflux, Wormhole (Autobahn) |
| **HTTP polling / long-poll** | Stateless server; simple | Higher latency; more overhead; poor for real-time ICE candidate exchange | None |
| **Raw TCP** | Minimal overhead; simple | Doesn't work through HTTP proxies; custom framing needed | Croc (custom TCP rooms) |
| **gRPC streaming** | Typed messages; bidirectional streaming; code generation | Heavier dependency; HTTP/2 based (not universally proxy-friendly); overkill for this use case | None |

**Key considerations:**
- ICE candidate exchange needs low-latency bidirectional messaging — WebSocket is the natural fit
- All three reference projects use TCP-based signaling (WebSocket or custom TCP)
- WebSocket works through corporate HTTP proxies, which is important for wani's "works everywhere" goal
- The signaling server is also where PAKE/SPAKE2 messages transit, so it needs to be reliable

---

## Decision 6: Key Exchange & Authentication

`Status: DECIDED → SPAKE2 (RFC 9382) + QUIC TLS (Option B: identity proof inside QUIC)`

How peers verify each other's identity and establish encryption keys. This is central to wani's "identity via key exchange" requirement.

| Option | Pros | Cons | Prior Art |
|--------|------|------|-----------|
| **SPAKE2 (RFC 9382) + QUIC TLS** | Standardized PAKE; human-shareable code → strong session key; proven; identity verification independent of transport | Two encryption layers (SPAKE2-derived key + QUIC TLS); more protocol complexity | Wormhole |
| **PAKE (custom) + QUIC TLS** | Same concept as SPAKE2 but more implementation flexibility | Non-standardized; croc's implementation had a vulnerability; less audited | Croc |
| **QUIC TLS only (join code as session proof)** | Simplest; one encryption layer; QUIC handles everything | Join code is just a session ID, not a cryptographic input; no identity proof beyond "knows the code"; server must be trusted to not impersonate | Thruflux |
| **Noise XX + QUIC** | Mutual key exchange; PFS per connection; well-specified patterns; identity via public keys | More complex; requires persistent keypairs (or generate per session); may duplicate QUIC TLS functionality | Wormhole (Dilation) |
| **SPAKE2 for session key → use that key inside QUIC** | SPAKE2 establishes shared secret before QUIC handshake; QUIC uses pre-shared key mode | Tight coupling between PAKE and QUIC; PSK mode is less common in QUIC libraries | None of the three |

**Key considerations:**
- Wani's requirement: "Identity via key exchange." This means the pairing code should be a *cryptographic* input, not just a session address. This rules out Thruflux's approach (join code as session proof only).
- SPAKE2 is the strongest option: standardized (RFC), proven, and the pairing code directly contributes to key material. Even if someone intercepts the signaling server, they can't derive the session key without knowing the code.
- The question is how to layer SPAKE2 with QUIC:
  - **Option A:** SPAKE2 over signaling → derive shared secret → use QUIC with PSK (pre-shared key) derived from SPAKE2. Tight integration; one encryption layer in practice.
  - **Option B:** SPAKE2 over signaling → derive shared secret → establish QUIC with normal TLS → encrypt file data with SPAKE2-derived key inside QUIC. Two layers, but simpler to implement since QUIC "just works" without PSK mode.
  - **Option C:** SPAKE2 over signaling → verify identity → then let QUIC TLS handle all encryption. SPAKE2 is used for identity only, not for data encryption. Simplest, but the QUIC TLS key is independent of the pairing code.

---

## Decision 7: Pairing Code Design

`Status: DECIDED → Human-pronounceable words (longer, ~33-44 bits)`

The code that sender and receiver share to find each other and establish trust.

| Option | Entropy | Format Example | Pros | Cons | Prior Art |
|--------|---------|----------------|------|------|-----------|
| **Human-pronounceable words (short)** | ~16-24 bits | `7-crossover-clockwork` | Easy to say aloud; easy to type; tab-completable | Low entropy if attacker controls signaling | Wormhole |
| **Human-pronounceable words (longer)** | ~33-44 bits | `blue-hammer-ocean-tiger` | Still speakable; reasonable entropy | Longer to communicate | Croc (6+ chars) |
| **Alphanumeric (high entropy)** | ~80-95 bits | `A3F7-KX9M-2BPQ-R5TN` | Brute-force infeasible; machine-friendly | Hard to say aloud; typo-prone; must copy-paste | Thruflux |
| **Hybrid: words + numeric pin** | ~40-50 bits | `tiger-castle-7429` | Balance of speakability and entropy | Slightly longer | None |
| **QR code primary** | Unlimited | [QR image] | No typing; high entropy; works in person | Requires camera; doesn't work over phone/text | Croc (optional) |

**Decision note:** Default code is 4 words (~33-44 bits), e.g. `blue-hammer-ocean-tiger`. With an honest signaling server the attacker gets one guess per attempt, so even 24 bits is safe — but the longer format hedges against a compromised server attempting offline SPAKE2 guessing. Consider a `--words N` flag so the length is configurable; default stays at 4 words.

---

## Decision 8: File Transfer Design

`Status: DECIDED → Manifest-first`

How file metadata and data are structured during transfer.

| Option | Pros | Cons | Prior Art |
|--------|------|------|-----------|
| **Manifest-first** | Receiver knows full file list + sizes before transfer starts; enables resume, pre-allocation, early abort, accurate progress | Slight startup latency (scan + send manifest); manifest size limit for huge directories | Thruflux |
| **Stream-as-you-go** | Transfer starts immediately; no upfront scan | No progress bar until done scanning; can't pre-allocate; resume is harder; receiver can't reject before data flows | Croc (partial — knows file name/size but not full tree) |
| **Offer/answer negotiation** | Receiver can inspect and selectively accept files | Extra round-trip before transfer; more protocol complexity | Wormhole (offer message → answer) |

**Sub-decisions:**

### 8a: Compression

`Status: DECIDED → No compression for MVP; protocol reserves a compression field for future use`

| Option | Pros | Cons | Prior Art |
|--------|------|------|----------|
| **Optional, client-side** | User control; no overhead if not wanted; works for text-heavy transfers | Requires CPU; slows fast networks | Croc (default on) |
| **No compression** | Simpler pipeline; faster on fast networks | Wastes bandwidth on compressible files | Thruflux |
| **Adaptive (compress if ratio > threshold)** | Best of both worlds | More complex; needs sampling/heuristic | None |

**Decision note:** No compression in MVP — skip the complexity. The manifest entry for each file should include a `compression` field (e.g., enum: `none`, `zstd`) so the protocol supports it without a breaking change later. Sender sets `none` for all files for now; receiver checks the field before writing.

### 8b: Resume Strategy

`Status: DECIDED → Per-file for MVP; per-chunk as a future upgrade`

| Option | Pros | Cons | Prior Art |
|--------|------|------|----------|
| **Per-file (manifest tracks completed files)** | Simple; skip already-transferred files | Must resend partially-transferred file from beginning | Thruflux |
| **Per-chunk (hash-verified chunks)** | Fine-grained; resume mid-file | More metadata tracking; chunk size decisions | Croc |
| **No resume** | Simplest | Frustrating for large transfers on unstable connections | Wormhole |

**Decision note:** Per-file resume for MVP — the manifest marks each file as `pending` / `complete`, so an interrupted transfer restarts at the next incomplete file. This is a deliberate stepping stone: the manifest + file-status design should be modular enough that per-chunk can be layered on top without redesigning the protocol. Design chunk size as a configurable constant from the start.

### 8c: Integrity Verification

`Status: DECIDED → Both (QUIC AEAD in-transit + per-file xxHash end-to-end)`

| Option | Pros | Cons | Prior Art |
|--------|------|------|----------|
| **QUIC AEAD only (implicit)** | Zero overhead; every frame is already authenticated | No end-to-end file hash; corruption detectable per-frame but not per-file | Thruflux |
| **Per-file hash (SHA256/xxHash)** | Receiver can verify complete file; detect any corruption | Hash computation overhead (minor for SHA256, negligible for xxHash) | Croc (xxHash), Wormhole (SHA256) |
| **Both** | Belt and suspenders | Slightly more complexity | None |

**Decision note:** Both layers serve different failure modes. QUIC AEAD catches in-transit bit-flips at the frame level. The per-file xxHash (computed by sender as the manifest is built, verified by receiver after the file is fully written) catches disk write errors, silent corruption, and verifies the complete transfer. xxHash is ~10× faster than SHA256 and adds negligible CPU overhead — the implementation is about 5 lines of code per file. Use `xxhash.Sum64` from `github.com/cespare/xxhash`.

---

## Decision 9: Server Architecture

`Status: DECIDED → Single binary (signaling + STUN + built-in TURN via pion/turn); planned migration path to coturn`

How wani-server is structured and what services it bundles.

| Option | Pros | Cons | Prior Art |
|--------|------|------|-----------|
| **Single binary: signaling + STUN + TURN relay** | One thing to deploy; simple ops; single address for clients | Monolithic; TURN is bandwidth-heavy and may need separate scaling | None (all reference projects separate signaling from relay to some degree) |
| **Single binary: signaling + STUN; separate TURN (coturn)** | Leverage existing battle-tested TURN server; signaling stays lightweight | Two things to deploy; coturn configuration is complex | Thruflux |
| **Signaling only; use public STUN (e.g., Google/Cloudflare); separate relay** | Lightest server; STUN is free from public providers | Depends on third-party STUN; relay still needed | None |
| **Single binary with optional built-in relay** | Signaling + STUN always; relay enabled by flag; deploy one or two instances depending on scale | More code in server binary; relay mode needs bandwidth planning | None |

**Decision note:** Use `pion/turn` embedded in wani-server, enabled via a `--relay` flag. wani-server issues per-session HMAC credentials tied to the SPAKE2 pairing code that expire when the transfer ends — this is cleaner than coturn's static credential model. Public STUN servers (`stun.cloudflare.com`, `stun.l.google.com`) are used as defaults so no self-hosted STUN is needed. The ICE client code does not change when migrating to coturn later; only the server-side credential issuance and relay process change. Migration trigger: relay load becomes a scaling concern or open-relay risk of a custom implementation becomes unacceptable in production.

---

## Decision 10: Client Architecture

`Status: DECIDED → Core library + CLI frontend; planned migration path to fully decoupled REST daemon model`

How the wani-client is structured internally.

| Option | Pros | Cons | Prior Art |
|--------|------|------|-----------|
| **Monolithic CLI** | Simplest; one binary; direct control flow | Hard to add GUI later; logic coupled to terminal I/O | Croc |
| **Core library + CLI frontend** | Clean separation; library reusable for GUI/mobile later; testable | Slightly more upfront design; API boundary decisions | Wormhole (library + CLI) |
| **Transfer engine + REST API + CLI/GUI frontends** | Full decoupling; GUI is just an HTTP client; SSE for real-time progress; multiple frontends trivial | More moving parts; local HTTP server overhead; over-engineered for MVP? | Thruflux |

**Decision note:** Transfer logic lives in an internal `wani/core` (or `internal/transfer`) package with a clean Go API — no terminal I/O, no flag parsing, no `fmt.Println`. The CLI is a thin consumer of that API. This keeps core logic independently testable and leaves the GUI path open: a future GUI frontend just imports the same package. Migration trigger to REST daemon model: if a GUI or browser-based frontend is actively being built, at that point wrapping the core in a local HTTP server + SSE progress stream (Thruflux's model) is a one-sprint addition without touching the core logic at all.

---

## Decision 11: Identity Model

`Status: DECIDED → Ephemeral per-transfer (MVP); abstract Identity interface designed in for persistent keypairs (ponds)`

How peers are identified — this affects both the security model and future pond design.

| Option | Pros | Cons | Prior Art |
|--------|------|------|-----------|
| **Ephemeral per-transfer (SPAKE2 from pairing code)** | No persistent state; privacy-preserving; simple; no key management | Can't recognize "I've talked to this person before"; no trust continuity | Croc, Wormhole |
| **Persistent keypairs (Ed25519) + pairing code for first contact** | "Trust on first use" — after first transfer, peers can recognize each other; foundation for ponds | Requires key storage; key management UX; more complex | None of the three (but libp2p uses this model) |
| **Server-assigned identity (accounts)** | Familiar UX; server can mediate trust | Requires accounts; contradicts "no accounts" requirement; centralized | None |
| **Hybrid: ephemeral by default, opt-in persistent keys** | Best of both worlds; simple default; power users get contacts | More code paths; "contacts" feature scope creep | None |

**Decision note:** MVP identity is fully ephemeral — the peer's identity is derived entirely from the SPAKE2 exchange for that transfer and discarded afterward. No key files, no contacts, no persistent state. However, the core library exposes an `Identity` interface (e.g., `Sign([]byte) []byte`, `PublicKey() []byte`, `Verify([]byte, []byte) bool`) rather than embedding SPAKE2 key material directly into the transfer logic. The MVP ships one implementation: `EphemeralIdentity` (keys derived from SPAKE2, zero-lifetime). The pond phase adds a second implementation: `PersistentIdentity` (Ed25519 keypair loaded from disk, trust-on-first-use like SSH). No transfer logic needs to change — it only ever talks to the interface.

---

## Decision 12: Encryption Layering

`Status: DECIDED → SPAKE2 (identity) + QUIC TLS (transport); resolved by Decision 6`

How many encryption layers and where they live. Related to Decision 6.

| Option | Layers | Pros | Cons |
|--------|--------|------|------|
| **QUIC TLS 1.3 only** | 1 | Simplest; no redundant encryption; QUIC handles everything | Identity not tied to pairing code; server impersonation possible if TLS certs aren't verified | 
| **SPAKE2 (identity) + QUIC TLS (transport)** | 2 | Identity proven independent of transport; defense in depth; even if QUIC TLS is somehow compromised, SPAKE2 layer protects data | Double encryption overhead (minor); more protocol complexity |
| **SPAKE2 → QUIC PSK mode** | 1 | SPAKE2-derived key feeds directly into QUIC as pre-shared key; single encryption layer but identity-bound | PSK support varies across QUIC libraries; tighter coupling |
| **QUIC TLS + Noise (for ponds)** | 2 | PFS per-connection for long-lived pond channels; QUIC for transport, Noise for session | Only relevant for ponds; overkill for ephemeral transfers |

**Decision note:** This decision is resolved by Decision 6 (Option B). SPAKE2 runs over the signaling WebSocket and derives shared secret K. QUIC is established with normal TLS (no PSK mode needed). The first message inside the QUIC connection is an HMAC over a known string using K — this proves both peers completed the same SPAKE2 exchange without adding another encryption layer. Data encryption is handled entirely by QUIC TLS 1.3 (AES-GCM or ChaCha20-Poly1305 AEAD). The Noise / QUIC TLS + Noise option is deferred to the pond phase if long-lived channels need PFS beyond what QUIC's session resumption provides.

---

## Decision 13: Development & Deployment Environment

`Status: DECIDED → Cloud VM (DigitalOcean/Oracle Free Tier) + SCP deploy + localhost for daily dev + two-device setup for NAT testing`

How wani-server is hosted during development and testing, and how the development workflow handles a networked application that requires real NAT traversal to test properly.

**Constraint:** No ability to self-host. wani-server must run on a VM or cloud service.

| Option | Pros | Cons | Cost |
|--------|------|------|------|
| **Cloud VM (DigitalOcean/Vultr/Linode)** | Full control; static public IP; can run wani-server + TURN; closest to production; SSH access for debugging | Monthly cost (~$4-6/mo for small VM); manual setup; need to manage server | ~$5/mo |
| **Oracle Cloud Free Tier** | Free forever (1 ARM VM with 24GB RAM, or 2 AMD VMs with 1GB each); public IP; suitable for light relay traffic | Limited regions; ARM may need cross-compilation; less community support; signup can be finnicky | Free |
| **AWS/GCP/Azure student credits** | Often free credits through .edu email or GitHub Student Pack; professional-grade infrastructure | Credit limits; more complex to configure; overkill for a single VM | Free (credits) |
| **Fly.io / Railway / Render** | Easy deployment (push to deploy); free tiers available; managed TLS | Less control over networking; may not support raw UDP (needed for STUN/TURN); more abstraction | Free tier or ~$5/mo |
| **GitHub Codespaces / Gitpod** | Cloud dev environment; consistent setup; accessible from any machine | Not suitable as a persistent server; meant for development sessions; no static IP | Free tier |

**Sub-decisions:**

### 13a: Testing Real NAT Traversal During Development

Testing P2P hole punching requires clients behind *different* NATs. Options:

| Option | What It Tests | Limitation |
|--------|--------------|------------|
| **Two machines on different networks** (e.g., laptop on WiFi + phone on cellular hotspot) | Real NAT traversal; closest to production | Requires two devices; manual testing |
| **Docker Compose with simulated NATs** | Automated; reproducible; CI-friendly; can simulate different NAT types | Complex Docker networking setup; doesn't test real-world NAT quirks |
| **Localhost loopback (both clients on same machine)** | Basic protocol correctness; signaling; encryption; file transfer logic | Does NOT test NAT traversal at all; useful for unit/integration tests only |
| **Cloud VM as one peer + local machine as other peer** | Tests real NAT (your local machine is behind NAT, VM has public IP) | Only tests one direction of NAT; doesn't test hole punching between two NATted peers |
| **Two cloud VMs in different regions** | Tests latency, throughput, relay performance | No NAT involved (both have public IPs); tests transfer but not traversal |

**Recommended development workflow:**
1. **Daily development:** Localhost loopback for fast iteration on protocol, encryption, file transfer logic
2. **Integration testing:** Cloud VM running wani-server; local machine as client A; phone/hotspot/second network as client B — tests real signaling + ICE + NAT
3. **Relay testing:** Cloud VM runs wani-server with relay; both clients behind NAT but with symmetric NAT simulated (or both on same network but connecting via relay)
4. **CI (stretch goal):** Docker Compose environment with simulated NAT for automated regression tests

### 13b: Deployment Method

| Option | Pros | Cons |
|--------|------|------|
| **SCP/rsync binary to VM** | Simplest; no infrastructure; build locally, copy up | Manual; no rollback; no CI |
| **Docker on VM** | Reproducible; easy to update; `docker pull` + `docker run` | Docker install on VM; slightly more setup; image registry needed |
| **GitHub Actions → deploy to VM** | Automated on push; build + test + deploy pipeline | More setup upfront; SSH key management; but pays off quickly |
| **Git clone + build on VM** | No cross-compilation issues; simple | Requires Go toolchain on VM; slower deploy |

**Decision note:** Deploy wani-server by SCP-ing the compiled binary to a cheap VM (DigitalOcean $5/mo or Oracle Cloud Free Tier). No Docker, no CI/CD for MVP — add those if the project grows. Daily development uses localhost loopback (both client instances on the same machine) for fast iteration on protocol, encryption, and file transfer logic. Real NAT traversal testing uses two separate devices on different networks: primary dev machine on home WiFi + old laptop on a phone hotspot (or vice versa), both pointing at the cloud VM as wani-server. This is the only setup that tests actual hole punching between two NATted peers.

---

## Decision Summary

| # | Decision | Leaning | Confidence | Depends On |
|---|----------|---------|------------|------------|
| 1 | Programming Language | **Go** | **Decided** | — |
| 2 | P2P Transport | **QUIC** | **Decided** | 1 |
| 3 | NAT Traversal | **Full ICE** | **Decided** | 1, 2 |
| 4 | Relay Fallback | **TURN + TCP fallback** | **Decided** | 2, 3 |
| 5 | Signaling Protocol | **WebSocket** | **Decided** | 1 |
| 6 | Key Exchange | **SPAKE2 + QUIC TLS (Option B)** | **Decided** | 2, 12 |
| 7 | Pairing Code Design | **Words, longer (~33-44 bits)** | **Decided** | 6 |
| 8 | File Transfer Design | **Manifest-first; no compression (extensible); per-file resume; both AEAD + xxHash** | **Decided** | 2 |
| 9 | Server Architecture | **Single binary (pion/turn embedded; `--relay` flag); coturn migration planned** | **Decided** | 3, 4 |
| 10 | Client Architecture | **Core library + CLI frontend; REST daemon migration planned** | **Decided** | 1 |
| 11 | Identity Model | **Ephemeral MVP; abstract `Identity` interface for persistent keypairs (ponds)** | **Decided** | 6 |
| 12 | Encryption Layering | **SPAKE2 (identity) + QUIC TLS (transport) — resolved by Decision 6** | **Decided** | 2, 6 |
| 13 | Dev & Deployment Environment | **Cloud VM + SCP deploy; localhost dev; old laptop + hotspot for NAT testing** | **Decided** | 1, 9 |
