# Wani — Workspace Instructions

## Project

Wani is a P2P file transfer tool. Direct transfers via NAT hole punching, relay fallback when direct fails. Identity via SPAKE2 key exchange from human-readable pairing codes.

## Tech Stack

- **Language:** Go
- **Transport:** QUIC (`github.com/quic-go/quic-go`)
- **NAT Traversal:** Full ICE (`github.com/pion/ice`)
- **Relay:** Built-in TURN (`github.com/pion/turn`) behind `--relay` flag
- **Signaling:** WebSocket
- **Key Exchange:** SPAKE2 (RFC 9382) + QUIC TLS (Option B: HMAC identity proof inside QUIC)
- **Pairing Codes:** 4-word human-pronounceable (~33-44 bits)
- **Integrity:** QUIC AEAD in-transit + per-file xxHash (`github.com/cespare/xxhash`)

## Key Files

- `architecture.md` — All 13 architecture decisions (decided choices + implementation notes)
- `wani_overview.md` — Project vision, ecosystem components, prior art
- `ROADMAP.md` — Phased implementation checklist (Phases 0–4)
- `research/` — Deep dives on croc, magic-wormhole, Thruflux
- `.github/instructions/` — Feature-specific implementation specs (on-demand)
- `.github/prompts/plan-feature.prompt.md` — Slash command to generate feature specs from architecture

## Package Layout

```
cmd/wani-server/    # Server entrypoint
cmd/wani-client/    # Client entrypoint
internal/server/    # Signaling, STUN, TURN server logic
internal/client/    # Core transfer library (no terminal I/O)
internal/protocol/  # Shared types: messages, manifest, pairing codes
internal/identity/  # Identity interface + EphemeralIdentity (SPAKE2)
```

## Agent Behavior

- **Do not auto-implement planned features.** If a reported issue is best solved by a feature already on the roadmap, stop and report: (1) what the root cause is, (2) which planned feature resolves it, and (3) any practical workarounds available right now. Do not begin implementing the planned feature without explicit instruction.

## Conventions

- Core library (`internal/client/`, `internal/protocol/`, `internal/identity/`) must have **zero** terminal I/O — no `fmt.Println`, no `os.Stdin`. Only `cmd/` packages talk to the terminal.
- Error handling: return errors, don't panic. Wrap with `fmt.Errorf("context: %w", err)`.
- Use `internal/` to enforce package boundaries — external consumers cannot import these.
