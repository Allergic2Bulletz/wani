# Wani — Codebase Tour

> A guided walkthrough of the Wani codebase, mapping architecture decisions to actual code.
> Written for someone who can read Go syntax but is new to Go-specific patterns.

---

## Table of Contents

1. [Project Structure](#1-project-structure)
2. [Go Concepts Used in This Codebase](#2-go-concepts-used-in-this-codebase)
3. [The Server: Signaling Hub](#3-the-server-signaling-hub)
4. [The Client: Core Library](#4-the-client-core-library)
5. [Pairing Codes & The Protocol Package](#5-pairing-codes--the-protocol-package)
6. [SPAKE2 Key Exchange](#6-spake2-key-exchange)
7. [ICE: Finding a Path Between Peers](#7-ice-finding-a-path-between-peers)
8. [QUIC: Encrypted Transport over UDP](#8-quic-encrypted-transport-over-udp)
9. [File Transfer: Manifests, Streams, and Hashing](#9-file-transfer-manifests-streams-and-hashing)
10. [The CLI: Tying It All Together](#10-the-cli-tying-it-all-together)
11. [Build System](#11-build-system)

---

## 1. Project Structure

```
cmd/
  wani-server/main.go        — Server binary entrypoint
  wani-client/main.go        — Client binary entrypoint
  wani-client/clipboard_*.go — Platform-specific clipboard support

internal/
  server/
    server.go                — HTTP server setup, routes
    ws.go                    — WebSocket handler, message dispatch
    sessions.go              — Session store: create, join, cleanup

  client/
    signaling.go             — WebSocket client for signaling server
    ice.go                   — ICE agent creation and candidate exchange
    quic.go                  — QUIC connection over ICE, identity proof
    manifest.go              — Build file manifest (scan + hash)
    transfer.go              — Send/receive files over QUIC streams
    stun.go                  — Standalone STUN discovery (for `stun` command)
    client.go                — Package declaration (currently empty)

  protocol/
    messages.go              — Wire format for all signaling messages
    protocol.go              — Manifest and FileEntry types
    codes.go                 — Pairing code generation and validation
    wordlist.txt             — 2048-word dictionary (embedded at compile time)

  identity/
    identity.go              — Identity interface
    ephemeral.go             — SPAKE2-based ephemeral identity
```

**Key rule:** Everything under `internal/` is a private Go package — it cannot be imported by code outside this module. This is enforced by the Go compiler, not convention. The `cmd/` packages are the only code that talks to the terminal (prints output, reads input, calls `os.Exit`). The `internal/` packages return errors and values — they never print.

---

## 2. Go Concepts Used in This Codebase

A few Go-specific patterns appear frequently. Here's what they mean:

### `defer`

```go
f, err := os.Open(path)
if err != nil { return err }
defer f.Close()
```

`defer` schedules a function call to run when the enclosing function returns. It's Go's equivalent of a `finally` block. You'll see `defer conn.Close()`, `defer cancel()`, `defer stream.Close()` everywhere — they guarantee cleanup happens even if the function exits early via an error return.

### `//go:embed`

```go
//go:embed wordlist.txt
var wordlistRaw string
```

This is a **compiler directive** (not a regular comment). It tells the Go compiler to read `wordlist.txt` at build time and embed its contents into the binary as a string variable. The file doesn't need to exist at runtime — it's baked into the executable. Used in `internal/protocol/codes.go` for the pairing code wordlist.

### `func init()`

```go
func init() {
    // parse wordlist...
}
```

`init()` runs automatically when a package is first imported, before `main()`. In `codes.go`, it parses the embedded wordlist string into a slice and a lookup map at program startup.

### `//nolint` comments

```go
quicConn.CloseWithError(0, "done") //nolint:errcheck
```

These are instructions to the Go linter to suppress specific warnings. `errcheck` means "I know this returns an error and I'm intentionally ignoring it." The project uses these sparingly for cleanup calls where there's nothing useful to do with the error.

### `//go:build` (build tags)

```go
//go:build windows || darwin
```

At the top of a `.go` file, this tells the compiler to only include this file when building for the listed platforms. `clipboard_desktop.go` compiles on Windows and macOS (where system clipboards exist); `clipboard_other.go` compiles on everything else (Linux servers, etc.) and provides a no-op stub.

### Interfaces

```go
type Identity interface {
    SharedSecret() []byte
}
```

Go interfaces are satisfied implicitly — there's no `implements` keyword. Any type with a `SharedSecret() []byte` method automatically satisfies the `Identity` interface. This lets the codebase swap identity implementations (ephemeral now, persistent later) without changing the code that uses them.

### Error wrapping

```go
return fmt.Errorf("signaling.Connect: %w", err)
```

`%w` wraps an error with additional context while preserving the original. Callers can use `errors.Is(err, someErr)` to check the underlying cause through any number of wrapping layers.

---

## 3. The Server: Signaling Hub

The server is minimal: an HTTP server with two endpoints.

### `internal/server/server.go` — Setup

```go
func New(ctx context.Context, addr string) *Server {
    mux := http.NewServeMux()
    store := NewSessionStore()
    s := &Server{addr: addr, mux: mux, store: store}
    mux.HandleFunc("/health", s.handleHealth)
    mux.HandleFunc("/ws", s.handleWS)
    store.StartCleanup(ctx)
    return s
}
```

- `/health` — simple HTTP 200 response (used by monitoring and CI/CD to verify the server is alive)
- `/ws` — WebSocket upgrade endpoint (all signaling happens here)
- `StartCleanup(ctx)` — launches a background goroutine that sweeps expired sessions every minute. The `ctx` parameter controls its lifetime: when the server shuts down and the context is cancelled, the cleanup goroutine stops.

### `internal/server/ws.go` — WebSocket Handling

When a client connects to `/ws`, the HTTP connection is upgraded to a WebSocket:

```go
func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
    conn, err := upgrader.Upgrade(w, r, nil)
    // ...
    defer func() {
        conn.Close()
        s.cleanupConn(conn)
    }()
    for {
        var msg protocol.SignalMessage
        if err := conn.ReadJSON(&msg); err != nil {
            return  // client disconnected
        }
        s.dispatch(conn, msg)
    }
}
```

The handler reads JSON messages in a loop and dispatches them by type:

| Message Type | Handler | What It Does |
|---|---|---|
| `create_session` | `handleCreateSession` | Generate pairing code, store sender connection |
| `join_session` | `handleJoinSession` | Look up session by code, attach receiver, notify both peers |
| `relay` | `handleRelay` | Forward opaque payload to the other peer (used for SPAKE2) |
| `ice_candidate` | `handleRelay` | Forward ICE candidate (server doesn't parse it) |
| `ice_credentials` | `handleRelay` | Forward ICE credentials (server doesn't parse them) |

The server is deliberately unintelligent about relay messages. It doesn't understand SPAKE2 blobs or ICE candidates — it just pipes bytes between the two connections in a session.

### `internal/server/sessions.go` — Session Lifecycle

The `SessionStore` is a thread-safe in-memory store:

```go
type SessionStore struct {
    mu       sync.RWMutex
    sessions map[string]*Session       // pairing code → session
    connToCode map[*websocket.Conn]string  // reverse lookup: connection → code
    connToSide map[*websocket.Conn]connSide // connection → sender/receiver
}
```

Each `Session` holds references to both WebSocket connections plus per-connection write mutexes (gorilla/websocket requires that only one goroutine writes to a connection at a time).

**Session lifecycle:**
1. **Created** when sender calls `create_session` — TTL 5 minutes
2. **Ready** when receiver calls `join_session` — both peers notified
3. **Receiver disconnect** — session enters dormancy for 15 minutes (receiver can rejoin with same code for transfer resume)
4. **Sender disconnect** — session deleted immediately (no point keeping it)
5. **Expired** — cleanup goroutine deletes sessions past their TTL

---

## 4. The Client: Core Library

All client logic lives in `internal/client/`. The package exports functions that the CLI calls — it never prints or reads from stdin.

### `internal/client/signaling.go` — Talking to the Server

`SignalingClient` wraps a WebSocket connection to the signaling server:

```go
type SignalingClient struct {
    conn *websocket.Conn
}
```

Key methods map directly to protocol phases:

| Method | Used By | Purpose |
|---|---|---|
| `Connect()` | Both | Dial the signaling server |
| `CreateSession()` | Sender | Request new session, get pairing code |
| `JoinSession(code)` | Receiver | Join existing session by code |
| `WaitForPeer(ctx)` | Sender | Block until receiver joins |
| `ExchangeSPAKE2(id)` | Both | Run full SPAKE2 handshake via relay messages |
| `PingPong(id)` | Sender | Send authenticated ping, verify pong |
| `WaitAndPong(id)` | Receiver | Verify ping, send authenticated pong |
| `SendICECredentials()` | Both | Send local ICE ufrag/pwd to peer |
| `ReadICECredentials()` | Both | Receive peer's ICE ufrag/pwd |
| `SendICECandidate()` | Both | Send one ICE candidate (or nil sentinel) |
| `ReadICECandidate()` | Both | Receive one ICE candidate from peer |

The `readUntil(wantType)` helper reads messages in a loop, discarding unrecognised types, until it sees the expected message type or an error. This keeps the signaling reads sequential — each phase reads exactly the messages it expects.

### HMAC helpers

Both `signaling.go` and `quic.go` compute HMACs:

```go
func computeHMAC(key []byte, label string) []byte {
    mac := hmac.New(sha256.New, key)
    mac.Write([]byte(label))
    return mac.Sum(nil)
}
```

Different labels distinguish different proofs: `"wani-ping"`, `"wani-pong"`, and `"wani-quic-verify"`. Same key K, different label → different HMAC output. This prevents replay (you can't reuse a ping HMAC as a QUIC verification).

---

## 5. Pairing Codes & The Protocol Package

### `internal/protocol/codes.go` — Code Generation

The wordlist (2048 words, 11 bits per word) is embedded at compile time:

```go
//go:embed wordlist.txt
var wordlistRaw string
```

`GenerateCode(n)` picks `n` random words using `crypto/rand` (cryptographically secure randomness, not `math/rand`) and joins them with hyphens. Default is 4 words = 44 bits of entropy.

`ValidateCode(code)` splits on hyphens and checks every word against a precomputed set. The server uses this to reject malformed codes before doing a session lookup.

### `internal/protocol/messages.go` — Wire Format

All signaling messages use a single struct:

```go
type SignalMessage struct {
    Type    string `json:"type"`
    Code    string `json:"code,omitempty"`
    Payload []byte `json:"payload,omitempty"`
    Message string `json:"message,omitempty"`
}
```

The `omitempty` JSON tag means empty fields are omitted from the wire — a `relay` message only has `type` and `payload`, the rest are absent. The `Payload` field holds raw bytes; Go's `encoding/json` automatically base64-encodes `[]byte` fields in JSON.

### `internal/protocol/protocol.go` — Manifest Types

```go
type FileEntry struct {
    Path        string      `json:"path"`
    Size        int64       `json:"size"`
    XXHash      uint64      `json:"xxhash"`
    Compression Compression `json:"compression"`  // always "none" for now
}

type Manifest struct {
    Files    []FileEntry `json:"files"`
    RootName string      `json:"root_name,omitempty"`
}
```

`RootName` is set when sending a directory — the receiver recreates this directory under its output path. For single files, it's empty.

`ManifestResponse` carries the receiver's "ready" status and a list of already-completed files (for resume support).

---

## 6. SPAKE2 Key Exchange

### `internal/identity/ephemeral.go`

`EphemeralIdentity` wraps the SPAKE2 protocol:

```go
type EphemeralIdentity struct {
    role   Role              // RoleSender or RoleReceiver
    spake  *spake2go.SPAKE2  // the library's state machine
    secret []byte            // derived shared key (set after exchange)
}
```

`NewEphemeralIdentity(role, password)` creates the SPAKE2 context. The `password` is the raw pairing code string (e.g., `"blue-hammer-ocean-tiger"` as bytes). The sender is SPAKE2 "Client" (Party A); the receiver is SPAKE2 "Server" (Party B).

`RunExchange(send, recv)` accepts two closures — functions for sending bytes and receiving bytes. The caller (signaling.go) wires these to the WebSocket relay channel. This decouples the cryptographic protocol from the transport:

```go
// In signaling.go:
send := func(payload []byte) error {
    return sc.conn.WriteJSON(protocol.SignalMessage{Type: "relay", Payload: payload})
}
recv := func() ([]byte, error) {
    msg, err := sc.readUntil("relay")
    return msg.Payload, err
}
id.RunExchange(send, recv)
```

The exchange is 4 messages (Start → Exchange → Finish → Confirm), after which both sides hold an identical shared secret K. `SharedSecret()` returns this key; it's used for ping-pong verification and QUIC identity proof.

### `internal/identity/identity.go`

A simple interface:

```go
type Identity interface {
    SharedSecret() []byte
}
```

Today only `EphemeralIdentity` implements this. The interface exists so that a future `PersistentIdentity` (Ed25519 keypair from disk) can be swapped in without changing any transfer code.

---

## 7. ICE: Finding a Path Between Peers

### `internal/client/ice.go`

ICE (Interactive Connectivity Establishment) is a framework for testing every possible network path between two peers and selecting the best one.

**Agent creation:**

```go
func NewICEAgent(stunServers []*stun.URI) (*ice.Agent, error) {
    agent, err := ice.NewAgentWithOptions(
        ice.WithUrls(stunServers),                      // STUN servers for public IP discovery
        ice.WithNetworkTypes([]ice.NetworkType{ice.NetworkTypeUDP4}),
        ice.WithCandidateTypes([]ice.CandidateType{
            ice.CandidateTypeHost,                      // local IP addresses
            ice.CandidateTypeServerReflexive,           // public IP via STUN
        }),
    )
    return agent, err
}
```

The agent gathers two types of candidates:
- **Host candidates** — local network interfaces (e.g., `192.168.1.5:54321`)
- **Server-reflexive candidates** — public IP:port learned from STUN (e.g., `73.45.12.99:61000`)

When TURN relay is added (Phase 4), relay candidates will appear here too.

**`GatherAndConnect` — full ICE exchange:**

This function drives the entire ICE process:

1. **Register candidate callback** — pion/ice calls this function for each discovered candidate, and calls it with `nil` when gathering is done
2. **Start gathering** — the ICE agent contacts STUN servers and enumerates local interfaces
3. **Wait for gathering to finish** — blocks on a channel
4. **Send local credentials** (ICE ufrag/pwd — random strings pion generates) via signaling
5. **Send all local candidates** via signaling, then a nil sentinel ("I'm done gathering")
6. **Read remote credentials** from signaling
7. **Read remote candidates** from signaling until nil sentinel
8. **Connect** — the sender calls `agent.Dial()` (controlling); the receiver calls `agent.Accept()` (controlled)

The gather-then-send order (all local candidates gathered before any are sent) keeps the signaling reads sequential and avoids both sides writing simultaneously.

The returned `*ice.Conn` is a Go `net.Conn` (stream interface) that wraps the winning UDP path.

### `internal/client/stun.go` — Standalone STUN

This is a simpler STUN client used by the `wani-client stun` command to show your public IP. It makes a single STUN binding request and extracts the XOR-MAPPED-ADDRESS. Not used during actual transfers (ICE handles STUN internally).

---

## 8. QUIC: Encrypted Transport over UDP

### `internal/client/quic.go`

QUIC provides TCP-like reliability (ordered delivery, retransmission, flow control) over UDP packets. It also includes mandatory TLS 1.3 encryption and supports multiplexed independent streams.

**The adapter problem:**

quic-go expects a `net.PacketConn` (UDP socket) to send and receive packets. pion/ice gives us an `*ice.Conn` (a `net.Conn` stream). The `icePacketConn` adapter bridges this gap:

```go
type icePacketConn struct {
    *ice.Conn
}

func (p *icePacketConn) ReadFrom(b []byte) (int, net.Addr, error) {
    n, err := p.Conn.Read(b)
    return n, p.Conn.RemoteAddr(), err
}

func (p *icePacketConn) WriteTo(b []byte, _ net.Addr) (int, error) {
    return p.Conn.Write(b)
}
```

`WriteTo` ignores the destination address — ICE handles all routing internally. This adapter is the glue between the two libraries.

**Note:** Because quic-go can't access the underlying OS UDP socket (it only sees `icePacketConn`), it can't enlarge the receive buffer from ~200 KB to the optimal 7 MB. This is logged as a warning at startup and is a known performance limitation documented in `TODO.md`.

**`DialQUIC` (sender side):**

```go
func DialQUIC(ctx context.Context, iceConn *ice.Conn, sharedKey []byte) (*quic.Conn, error) {
    pktConn := &icePacketConn{iceConn}
    tlsCfg := &tls.Config{
        InsecureSkipVerify: true,  // trust is via HMAC, not certificates
        NextProtos:         []string{"wani"},
    }
    conn, err := quic.Dial(ctx, pktConn, iceConn.RemoteAddr(), tlsCfg, quicConfig)
    // ...
    // Open first stream, exchange HMAC proofs
    stream, _ := conn.OpenStreamSync(ctx)
    proof := quicHMAC(sharedKey)
    stream.Write(proof)           // send our proof
    io.ReadFull(stream, peerProof) // read peer's proof
    hmac.Equal(peerProof, proof)   // verify
}
```

`InsecureSkipVerify: true` skips TLS certificate validation — this is safe because identity is verified by the HMAC exchange, not by the certificate. The receiver generates a throwaway self-signed certificate (in `generateEphemeralCert()`) just to satisfy TLS's requirement that the listener has a cert.

**`ListenQUIC` (receiver side):**

Mirror of DialQUIC but as the listener:
1. Generate ephemeral P-256 ECDSA certificate
2. Create a QUIC listener on the `icePacketConn`
3. Accept the incoming QUIC connection
4. Accept the first stream
5. Read peer's HMAC proof, verify, send own proof

**Identity proof flow:**

Both sides compute `HMAC-SHA256(K, "wani-quic-verify")`. Since K was derived from SPAKE2 (which requires knowing the pairing code), successfully matching HMACs proves: "I'm talking to someone who knew the same pairing code." This works even if the signaling server is compromised — the server never learns K.

**QUIC configuration:**

```go
var quicConfig = &quic.Config{
    MaxIdleTimeout: 10 * time.Second,
}
```

The idle timeout controls how quickly a dead peer is detected. Set to 10 seconds (default is 30s) so that if a receiver is killed, the sender's transfer call unblocks within ~10 seconds and can loop back to wait for a reconnect.

**`IsPeerClosedError`:**

When a peer disconnects (calls `CloseWithError(0, "done")`), the other side gets an `ApplicationError` with code 0 or an `IdleTimeoutError`. This function checks for those specific error types to distinguish "peer closed normally" from "transfer actually failed."

---

## 9. File Transfer: Manifests, Streams, and Hashing

### `internal/client/manifest.go` — Scanning Files

`BuildManifest(root)` walks a path and produces a `Manifest`:

- **Single file:** one entry with the filename as path
- **Directory:** walks recursively, records every regular file (skips symlinks, directories, special files)

For every file, it opens and reads the full contents through an xxHash-64 hasher:

```go
h := xxhash.New()
n, err := io.Copy(h, f)
return protocol.FileEntry{
    Path:   relPath,
    Size:   n,
    XXHash: h.Sum64(),
    Compression: "none",
}
```

Paths are normalized to forward slashes (`/`) for cross-platform compatibility (a manifest built on Windows works for a receiver on Linux).

### `internal/client/transfer.go` — Sending and Receiving

**Manifest exchange:**

- `SendManifest()` opens a QUIC stream, JSON-encodes the manifest, closes the write side, and reads back a `ManifestResponse`
- `ReceiveManifest()` accepts the stream, decodes the manifest, creates directories, loads resume state, and encodes back a response

**File transfer:**

`SendFiles()` iterates through the manifest. For each file not already completed:
1. Opens the file on disk
2. Opens a new QUIC stream
3. Copies the file data into the stream (via `io.Copy`)
4. Closes the write side (signals EOF to receiver)
5. Reads the receiver's ack (`"ok"` or an error message)

`ReceiveFiles()` mirrors this:
1. Accepts a QUIC stream
2. Creates the destination file
3. Copies from stream → `io.MultiWriter(file, xxhash)` (writes and hashes simultaneously)
4. Compares computed hash against manifest
5. Sends `"ok"` or an error message back
6. Appends the file path to `.wani-resume.json`

The `progressReader` wrapper intercepts `Read()` calls to report bytes transferred, which the CLI uses to update the progress bar.

**Resume state:**

`.wani-resume.json` is a simple JSON file in the destination directory:
```json
{"completed": ["file1.txt", "subdir/file2.mp4"]}
```

Loaded at the start of `ReceiveManifest()`, sent back to sender in the `ManifestResponse`. The sender skips files listed as completed. On full success, the file is deleted.

---

## 10. The CLI: Tying It All Together

### `cmd/wani-server/main.go`

Minimal:
```go
func run() error {
    flag.StringVar(&addr, "addr", ":8080", "address to listen on")
    flag.Parse()
    ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
    defer stop()
    s := server.New(ctx, addr)
    return s.ListenAndServe()
}
```

Parses flags, creates a signal-aware context (Ctrl+C triggers graceful shutdown), starts the server.

### `cmd/wani-client/main.go`

Three subcommands: `stun`, `send`, `receive`.

**`runSend()`** — the sender flow:

1. Parse flags, clean path
2. Connect to signaling server
3. `CreateSession()` → display pairing code, copy to clipboard (on desktop OS)
4. `BuildManifest()` — scan files before waiting (prep work)
5. **Re-wait loop** — if receiver disconnects and reconnects:
   - `WaitForPeer()` — blocks until receiver joins (5-minute timeout)
   - `ExchangeSPAKE2()` + `PingPong()` — identity verification
   - `NewICEAgent()` + `GatherAndConnect()` — NAT traversal
   - `DialQUIC()` — encrypted connection
   - `SendManifest()` — exchange file list
   - `SendFiles()` — stream data with progress bar
   - If `IsPeerClosedError`, check if signaling is still alive → loop back
6. On success: "Transfer complete: N file(s)"

**`runReceive()`** — the receiver flow:

1. Parse flags (including `-out` for destination directory)
2. Connect to signaling server
3. `JoinSession(code)` → verified against wordlist by server
4. `ExchangeSPAKE2()` + `WaitAndPong()` — identity verification
5. `NewICEAgent()` + `GatherAndConnect()` — NAT traversal
6. `ListenQUIC()` — accept encrypted connection
7. `ReceiveManifest()` — get file list, report completed files for resume
8. `ReceiveFiles()` — write files, verify hashes, show progress
9. On success: "Received N file(s) → /path"

**Timeouts:**
- 60-second context for setup phases (signaling + ICE + QUIC + manifest)
- The context is explicitly cancelled and replaced with `context.Background()` before file transfer starts, so slow transfers on limited bandwidth don't hit an arbitrary deadline

**Progress bars** use `schollz/progressbar` with per-file display: `[3/7] photos/img003.jpg 45% |████░░░░| (450MB/1GB, 3.5 MB/s) [2s:0s]`

### `cmd/wani-client/clipboard_*.go` — Clipboard Support

Two files with opposing build tags:
- `clipboard_desktop.go` (`//go:build windows || darwin`) — copies the pairing code to clipboard via `golang.design/x/clipboard`
- `clipboard_other.go` (`//go:build !windows && !darwin`) — no-op stub

The Go compiler includes exactly one of these files based on the target platform.

---

## 11. Build System

### `build.sh`

```bash
# Read server IP from config.json, inject into client binary
SERVER_IP=$(jq -r '."wani-server-ip"' config.json)
CLIENT_LDFLAGS="-X 'main.defaultServerURL=ws://${SERVER_IP}:8080/ws'"

# Build all targets
GOOS=linux GOARCH=amd64 go build -o build/wani-server ./cmd/wani-server
GOOS=linux GOARCH=amd64 go build -ldflags "$CLIENT_LDFLAGS" -o build/wani-client ./cmd/wani-client
GOOS=windows GOARCH=amd64 go build -ldflags "$CLIENT_LDFLAGS" -o build/wani-client.exe ./cmd/wani-client
```

- `-ldflags "-X 'main.defaultServerURL=...'"` is a Go linker flag that sets a string variable at build time. This bakes the server URL into the client binary so users don't need to pass `-server` every time.
- `GOOS`/`GOARCH` cross-compile for different platforms from any build machine.
- The server IP comes from `config.json` (not checked into git via `.gitignore`).

---

## Data Flow Summary

```
cmd/wani-client/main.go           — parses flags, drives the flow, shows progress
      |
      v
internal/client/signaling.go      — WebSocket to server: session, SPAKE2, ICE relay
      |
      v
internal/identity/ephemeral.go    — SPAKE2 key exchange → shared secret K
      |
      v
internal/client/ice.go            — pion/ice agent: STUN, candidates, hole punching
      |
      v
internal/client/quic.go           — QUIC over ICE conn, HMAC identity proof
      |
      v
internal/client/manifest.go       — scan files, compute hashes
internal/client/transfer.go       — stream files over QUIC, verify hashes, resume
      |
      v
internal/protocol/                — shared types (SignalMessage, Manifest, FileEntry, codes)
```

```
cmd/wani-server/main.go           — parses flags, starts server
      |
      v
internal/server/server.go         — HTTP setup, routes /health and /ws
internal/server/ws.go             — WebSocket handler, message dispatch, relay
internal/server/sessions.go       — session store, TTL, cleanup
      |
      v
internal/protocol/                — shared message types
```
