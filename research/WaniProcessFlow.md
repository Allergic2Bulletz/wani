# Wani — Process Flow (Client → Server → Client P2P Transfer)

> High-level sequence of events for a file transfer between two peers.
> This document covers the **direct P2P path** (NAT hole punching via ICE).
> Future additions: TURN relay transfer, pond-to-pond transfer.

---

## Overview

Wani is a peer-to-peer file transfer tool. Two clients communicate through a central **signaling server** to find each other and negotiate a connection, but the actual file data travels directly between the two peers over an encrypted QUIC connection. The signaling server never sees the files or the encryption key.

**Participants:**
- **Sender** — the client sending files
- **Server** — the signaling server (wani-server); brokers the connection
- **Receiver** — the client receiving files

---

## Phase 1: Session Setup

```
Sender                         Server                        Receiver
  |                              |                              |
  |  wani-client send ./files    |                              |
  |                              |                              |
  |-- WebSocket connect -------->|                              |
  |-- create_session ----------->|                              |
  |                              |  generates 4-word code       |
  |                              |  creates session record      |
  |<-- session_created ----------|                              |
  |   (code: "blue-hammer-      |                              |
  |    ocean-tiger")             |                              |
  |                              |                              |
  |  Scans files, builds         |                              |
  |  manifest (paths, sizes,     |                              |
  |  xxHash per file)            |                              |
  |                              |                              |
  |  Displays code to user       |                              |
  |  Waits for peer...           |                              |
```

The sender connects to the signaling server and requests a new session. The server generates a 4-word pairing code from a 2048-word embedded dictionary (44 bits of entropy) using `crypto/rand` and returns it. The sender displays this code and waits.

While waiting, the sender scans the target path and builds a **manifest** — a list of every file with its relative path, size, and xxHash-64 checksum. This happens before the connection is established so the CPU-bound work doesn't add latency later.

---

## Phase 2: Receiver Joins

```
Sender                         Server                        Receiver
  |                              |                              |
  |                              |   wani-client receive        |
  |                              |     blue-hammer-ocean-tiger  |
  |                              |                              |
  |                              |<-- WebSocket connect --------|
  |                              |<-- join_session (code) ------|
  |                              |                              |
  |                              |  Looks up session by code    |
  |                              |  Attaches receiver           |
  |                              |                              |
  |<-- session_ready ------------|-- session_ready ------------>|
  |                              |                              |
```

The receiver enters the code and connects to the same signaling server. The server matches the code to the sender's session and notifies both peers that the session is ready. From this point on, the server acts as a relay for signaling messages — it forwards opaque payloads between the two peers without inspecting them.

---

## Phase 3: SPAKE2 Key Exchange

```
Sender                         Server                        Receiver
  |                              |                              |
  |  Both know the code          |  Server does NOT know code   |
  |                              |                              |
  |-- SPAKE2 Start blob ------->|-- forward ------------------->|
  |                              |                              |
  |                              |<-- SPAKE2 Exchange blob -----|
  |<-- forward ------------------|                              |
  |                              |                              |
  |-- SPAKE2 Finish blob ------>|-- forward ------------------->|
  |                              |                              |
  |                              |<-- SPAKE2 Confirm blob ------|
  |<-- forward ------------------|                              |
  |                              |                              |
  |  [derives shared key K]      |                [derives K]   |
```

Both peers independently create a SPAKE2 context keyed to the pairing code (used as the password). They exchange four messages through the signaling server's `relay` channel. The server forwards these blobs opaquely — it cannot derive the shared key K because it never learns the pairing code.

The SPAKE2 library used (`backkem/spake2-go`, RFC 9382) has a 4-message flow: Start → Exchange → Finish → Confirm. This includes built-in key confirmation (both sides verify they derived the same key).

---

## Phase 4: Authenticated Ping-Pong

```
Sender                         Server                        Receiver
  |                              |                              |
  |-- HMAC-SHA256(K, "ping") -->|-- forward ------------------->|
  |                              |                  verifies HMAC|
  |                              |<-- HMAC-SHA256(K, "pong") ---|
  |<-- forward ------------------|                              |
  |  verifies HMAC               |                              |
  |                              |                              |
  |  "Paired successfully!"      |       "Paired successfully!" |
```

An explicit ping-pong over the signaling channel confirms the SPAKE2 exchange succeeded end-to-end. The sender computes `HMAC-SHA256(K, "wani-ping")` and sends it; the receiver verifies, then responds with `HMAC-SHA256(K, "wani-pong")`. If either HMAC doesn't match (wrong code → different K), the connection is dropped with "Pairing failed."

---

## Phase 5: ICE Negotiation (NAT Traversal)

```
Sender                         Server                        Receiver
  |                              |                              |
  |  Contacts STUN servers       |         Contacts STUN servers|
  |  (Cloudflare, Google)        |        (Cloudflare, Google)  |
  |  Learns public IP:port       |         Learns public IP:port|
  |  Knows local IP:port         |          Knows local IP:port |
  |                              |                              |
  |-- ICE credentials --------->|-- forward ------------------->|
  |-- ICE candidate (local) --->|-- forward ------------------->|
  |-- ICE candidate (public) -->|-- forward ------------------->|
  |-- ICE sentinel (done) ----->|-- forward ------------------->|
  |                              |                              |
  |                              |<-- ICE credentials ----------|
  |                              |<-- ICE candidate (local) ----|
  |                              |<-- ICE candidate (public) ---|
  |<-- forward ------------------|<-- ICE sentinel (done) ------|
  |                              |                              |
  |  Both sides try all candidate pairs                         |
  |  Direct path wins (or relay if needed)                      |
  |                              |                              |
  |<==================== UDP path established ================>|
```

Both peers create a pion/ice ICE agent, gather local and server-reflexive (STUN) candidates, then exchange them through the signaling server. The exchange is ordered: credentials first, then candidates, then a nil sentinel marking gathering-complete.

ICE runs connectivity checks on all candidate pairs. The best working pair wins — typically a direct UDP path through both NATs (hole-punched). If direct connectivity fails, it falls back to relay (TURN, Phase 4 of the roadmap — not yet implemented).

The sender is the "controlling" ICE agent (it dials); the receiver is "controlled" (it accepts).

---

## Phase 6: QUIC Connection + Identity Proof

```
Sender                                                    Receiver
  |                                                          |
  |-- QUIC handshake (TLS 1.3 over the UDP path) ---------->|
  |<-- QUIC handshake response ------------------------------|
  |                                                          |
  |  Opens first bi-directional QUIC stream                  |
  |                                                          |
  |-- HMAC-SHA256(K, "wani-quic-verify") ------------------->|
  |                    verifies: same K = same pairing code   |
  |<-- HMAC-SHA256(K, "wani-quic-verify") -------------------|
  |  verifies                                                |
  |                                                          |
  |  QUIC connection authenticated and encrypted             |
```

QUIC is established over the UDP path selected by ICE. The receiver generates an ephemeral self-signed TLS certificate (TLS certificate validation is skipped — trust is based on the SPAKE2-derived key, not PKI). Both peers exchange `HMAC-SHA256(K, "wani-quic-verify")` over the first QUIC stream to confirm they're talking to someone who completed the same SPAKE2 exchange. This prevents man-in-the-middle attacks even if the signaling server is compromised.

From this point on, all data is encrypted by QUIC's TLS 1.3 (AES-GCM or ChaCha20-Poly1305). The signaling server is no longer involved.

---

## Phase 7: Manifest Exchange

```
Sender                                                    Receiver
  |                                                          |
  |== QUIC stream: manifest JSON =========================>  |
  |   [{path, size, xxhash, compression}, ...]               |
  |                                                          |
  |   Receiver creates directory structure                   |
  |   Loads resume state (.wani-resume.json)                 |
  |                                                          |
  |  <== QUIC stream: ManifestResponse =====================|
  |      {status: "ready", completed: ["file1.txt", ...]}   |
```

The sender transmits the manifest over a new QUIC stream. The receiver:
1. Parses the manifest
2. Creates all required subdirectories
3. Checks for previous partial transfer state (`.wani-resume.json`)
4. Responds with "ready" and a list of files already completed (for resume)

---

## Phase 8: File Transfer

```
Sender                                                    Receiver
  |                                                          |
  |== QUIC stream: file1.jpg data ========================>  |
  |                             writes to disk, computes hash|
  |                             hash matches manifest → "ok" |
  |  <== "ok" ==============================================|
  |                                                          |
  |== QUIC stream: file2.mp4 data ========================>  |
  |  <== "ok" ==============================================|
  |                                                          |
  |  ... (one stream per file, sequential) ...               |
  |                                                          |
  |  All files done                                          |
  |  "Transfer complete: N file(s)"                          |
```

Each file is sent on its own QUIC stream. The sender streams the raw file bytes; the receiver simultaneously writes to disk and computes an xxHash-64 checksum. When the stream ends (sender closes write side), the receiver compares the hash against the manifest entry:

- **Match:** responds with `"ok"`, saves progress to `.wani-resume.json`
- **Mismatch:** responds with an error message, transfer aborts

On full completion, the resume state file is deleted.

---

## Resume Behavior

If the receiver disconnects mid-transfer:
1. The sender detects the broken QUIC connection (~10s idle timeout)
2. If the signaling WebSocket is still alive, the sender loops back to "Waiting for receiver..."
3. The server keeps the session alive for up to 15 minutes (receiver-disconnect TTL)
4. When a receiver reconnects with the same code, the full flow repeats from Phase 3
5. During manifest exchange, the receiver reports which files are already done
6. The sender skips completed files and resumes from the next pending file

---

## Summary of What Each Participant Sees

| Phase | Sender | Server | Receiver |
|-------|--------|--------|----------|
| 1. Setup | Scans files, gets code | Creates session | — |
| 2. Join | Notified peer arrived | Matches code | Connects, joins |
| 3. SPAKE2 | Derives key K | Forwards blobs (cannot derive K) | Derives key K |
| 4. Ping-Pong | Verifies K | Forwards HMACs | Verifies K |
| 5. ICE | Gathers & exchanges candidates | Forwards candidates | Gathers & exchanges candidates |
| 6. QUIC | Dials, proves identity | **Done — not involved** | Listens, proves identity |
| 7. Manifest | Sends file list | — | Receives, responds ready |
| 8. Transfer | Streams files | — | Writes files, verifies hashes |

**Key security property:** The signaling server facilitates the connection but never learns the pairing code, the shared key, or the file contents. Even a fully compromised server cannot decrypt the transfer.
