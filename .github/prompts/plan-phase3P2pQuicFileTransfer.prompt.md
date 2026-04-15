# Plan: Phase 3 — P2P QUIC Connection + File Transfer

## TL;DR
Phase 2a–2e is complete. Phase 3 establishes a real ICE+QUIC P2P connection, then layers manifest-first file transfer on top. Steps are 3a → 3b → 3c (checkpoint) → 3d → 3e. The 3c checkpoint produces a minimal but working CLI file transfer before adding resume and polish.

---

## Decisions
- **One QUIC stream per file** — cleanly maps to per-file resume in 3d; leverages QUIC multiplexing
- **Receiver `-out` flag** — default `.` (current directory); introduced at 3c checkpoint, carried through 3e
- **HMAC identity proof on first QUIC stream**: `HMAC-SHA256(K, "wani-quic-verify")` per architecture Decision 12
- **ICE credentials exchange**: new `MsgICECredentials = "ice_credentials"` message type; each peer sends its ufrag+pwd via `relay`-style opaque message after SPAKE2 completes
- **QUIC TLS**: receiver generates ephemeral self-signed ECDSA cert; sender uses `InsecureSkipVerify: true` (auth provided by HMAC proof over SPAKE2-derived K)
- **`icePacketConn` adapter**: `*ice.Conn` (implements `net.Conn`) wrapped as `net.PacketConn` so `quic.Transport` can use it — `ReadFrom` delegates to `ice.Conn.Read`, `WriteTo` delegates to `Write`, addr derived from `RemoteAddr()`
- **Manifest encoding**: JSON over the first QUIC stream (control stream); `ready` response is JSON `{"status":"ready","completed":[]}`
- **xxHash per file**: stream hasher (`xxhash.New()`) as bytes are written to disk; compare to manifest entry after file completes

---

## Phase 3a: ICE + QUIC Connection

**Goal**: Replace the `candidate:placeholder` stub with a real pion/ice agent; establish authenticated QUIC connection over ICE-selected path.

### Steps

1. Add Go dependencies:
   - `go get github.com/pion/ice/v4`
   - `go get github.com/quic-go/quic-go`

2. Add `MsgICECredentials = "ice_credentials"` to `internal/protocol/messages.go`

3. Add `SignalingClient.SendICECredentials(ufrag, pwd string) error` and `ReadICECredentials() (ufrag, pwd string, err error)` to `internal/client/signaling.go`
   - Uses `relay` message type (opaque payload) serialized as `{"ufrag":"...","pwd":"..."}`
   - Uses `readUntil(MsgICECredentials)` pattern

4. Create `internal/client/ice.go` — `ICEConn` wrapper:
   - `NewICEAgent(ctx context.Context, stunServers []string) (*ice.Agent, error)` — creates `ice.Agent` with `ice.AgentConfig{Urls, NetworkTypes: [UDP4], CandidateTypes: [Host, ServerReflexive]}`
   - `GatherAndConnect(ctx, agent *ice.Agent, sc *SignalingClient, role ice.Role) (*ice.Conn, error)`:
     - Registers `agent.OnCandidate` callback: serializes each candidate JSON and calls `sc.SendICECandidate`
     - Calls `agent.GatherCandidates()`
     - Gets local ufrag/pwd via `agent.GetLocalUserCredentials()`, sends via `sc.SendICECredentials`
     - Launches goroutine: reads remote candidates from `sc.ReadICECandidate()` in a loop and calls `agent.AddRemoteCandidate()`
     - Reads peer's ufrag/pwd via `sc.ReadICECredentials()`
     - Calls `agent.Accept(ctx, remoteUfrag, remotePwd)` (receiver) or `agent.Dial(ctx, remoteUfrag, remotePwd)` (sender)
     - Cancels remote-candidate goroutine after `ice.Conn` returned

5. Create `internal/client/quic.go`:
   - `icePacketConn` struct: wraps `*ice.Conn`, implements `net.PacketConn`
     - `ReadFrom`: calls `ice.Conn.Read(b)`, returns `(n, conn.RemoteAddr(), nil)`
     - `WriteTo`: calls `ice.Conn.Write(b)`, ignores `addr`
     - `SetDeadline`, `SetReadDeadline`, `SetWriteDeadline`, `LocalAddr`, `Close`: delegate to `ice.Conn`
   - `DialQUIC(ctx context.Context, iceConn *ice.Conn, sharedKey []byte) (quic.Connection, error)` (sender):
     - Wraps iceConn in `icePacketConn`
     - `tls.Config{InsecureSkipVerify: true, NextProtos: ["wani"]}`
     - `quic.Dial(ctx, pktConn, iceConn.RemoteAddr(), tlsCfg, nil)`
     - Opens stream 0, sends `HMAC-SHA256(sharedKey, "wani-quic-verify")`, reads peer's proof and verifies
   - `ListenQUIC(ctx context.Context, iceConn *ice.Conn, sharedKey []byte) (quic.Connection, error)` (receiver):
     - Generates ephemeral self-signed ECDSA cert (P-256, `crypto/ecdsa` + `crypto/x509`)
     - `tls.Config{Certificates: [...], NextProtos: ["wani"]}`
     - `quic.Listen(pktConn, tlsCfg, nil)` then `listener.Accept(ctx)`
     - Opens stream 0, receives sender's HMAC proof and verifies, sends own proof

6. Update `cmd/wani-client/main.go` `runSend` / `runReceive`:
   - After ping-pong: call `NewICEAgent` + `GatherAndConnect` (sender: `ice.RoleControlling`, receiver: `ice.RoleControlled`)
   - After ICE: call `DialQUIC` / `ListenQUIC`
   - Print "QUIC connected!" and exit (file transfer comes in 3b)

**Verification**: Two terminals on same machine — sender and receiver print "QUIC connected!" with no errors.

---

## Phase 3b: Manifest Protocol

**Goal**: Sender scans the file tree, sends a manifest; receiver prepares the directory structure and acknowledges.

### Steps (*parallel with 3a if desired, but depends on 3a QUIC conn type*)

1. Add `github.com/cespare/xxhash/v2` dependency

2. Add to `internal/protocol/protocol.go`:
   - `type Compression string` with constants `CompressionNone = "none"`
   - `type FileEntry struct { Path string; Size int64; XXHash uint64; Compression Compression }`
   - `type Manifest struct { Files []FileEntry }`
   - `type ManifestResponse struct { Status string; Completed []string }` — receiver's `ready` ack with optionally pre-completed files

3. Create `internal/client/manifest.go`:
   - `BuildManifest(root string) (*protocol.Manifest, error)` — `fs.WalkDir` over root; for each regular file: compute `xxhash.Sum64` by reading the file, record relative path; `Compression: "none"`
   - Paths stored as forward-slash relative paths (`filepath.ToSlash(rel)`)

4. Create `internal/client/transfer.go`:
   - `SendManifest(ctx context.Context, conn quic.Connection, m *protocol.Manifest) (*protocol.ManifestResponse, error)`:
     - Opens QUIC stream (stream 0 = control)
     - JSON-encodes manifest, writes to stream, closes write side
     - Reads JSON `ManifestResponse` from the stream
   - `ReceiveManifest(ctx context.Context, conn quic.Connection, destDir string) (*protocol.Manifest, *protocol.ManifestResponse, error)`:
     - Accepts QUIC stream
     - JSON-decodes manifest
     - `os.MkdirAll` for each unique directory in manifest
     - Sends `ManifestResponse{Status: "ready", Completed: []}` back
     - Returns manifest and response

5. Update `cmd/wani-client/main.go` to call manifest exchange after QUIC connects (sender sends manifest with a single placeholder file just for testing 3b)

**Verification**: Sender logs manifest sent, receiver logs "manifest received, N files, ready".

---

## Phase 3c: File Data Transfer

**Goal**: Stream files over QUIC, verify xxHash on receipt. Minimal CLI checkpoint.

### Steps

1. Add to `internal/client/transfer.go`:
   - `SendFiles(ctx context.Context, conn quic.Connection, m *protocol.Manifest, root string, progress ProgressFunc) error`:
     - For each file in manifest (in order): open QUIC stream, stream file bytes in chunks (e.g. 32KB), close stream when done
   - `ReceiveFiles(ctx context.Context, conn quic.Connection, m *protocol.Manifest, destDir string, progress ProgressFunc) error`:
     - For each file in manifest (in order): accept QUIC stream, write to destination path, compute `xxhash.New()` streaming hash as bytes arrive, compare hash to manifest entry after stream closes
     - Return `fmt.Errorf("file %s: xxHash mismatch: want %x got %x", ...)` on mismatch

2. Progress reporting: `type ProgressFunc func(file string, bytesWritten, totalBytes int64)` — caller in cmd/ handles printing.

---

### Checkpoint: Minimal CLI File Transfer Test

After 3c is complete, update `cmd/wani-client/main.go` to wire the full pipeline:

- **Sender `runSend()`**: add `-path` flag (positional arg or `-path`). Flow: connect → SPAKE2 → ping-pong → ICE+QUIC → `BuildManifest(path)` → `SendManifest` → `SendFiles` → print "X files transferred"
- **Receiver `runReceive()`**: add `-out` flag (default `.`). Flow: connect → SPAKE2 → ping-pong → ICE+QUIC → `ReceiveManifest(out)` → print manifest → `ReceiveFiles` → verify hashes → print "X files received"

**Verification checkpoint**: Transfer a directory of ~3 mixed files (small text + 1 larger binary) between two local terminals. Verify all xxHashes match. Confirm error path: corrupt a byte mid-stream → "xxHash mismatch" error.

---

## Phase 3d: Per-File Resume

*Depends on 3c checkpoint passing.*

### Steps

1. Create `internal/client/resume.go`:
   - `type ResumeState struct { Completed []string }` — list of completed relative file paths
   - `LoadResume(dir string) (*ResumeState, error)` — reads `.wani-resume.json` from `dir`; returns empty state if file not found
   - `SaveResume(dir string, state *ResumeState) error` — writes `.wani-resume.json` atomically (write temp file → rename)

2. Update `ReceiveFiles` in `transfer.go`:
   - Load resume state at start; skip files already in `state.Completed`
   - After each file successfully verified: add to state, call `SaveResume`

3. Update `SendManifest` / `ReceiveManifest` protocol:
   - `ManifestResponse.Completed` populated from `LoadResume` on receiver side
   - Sender receives `ManifestResponse`, filters already-completed files from send queue

**Verification**: Transfer 3 files. Kill sender mid-way through file 2. Restart with same code → file 1 is skipped, transfer resumes from file 2.

---

## Phase 3e: CLI UX

*Depends on 3d.*

### Steps

1. Update `cmd/wani-client/main.go`:
   - **Sender**: `wani-client send <path>` (positional path arg replacing `-path` flag); display pairing code prominently; progress bar using `ProgressFunc` callback; print summary on complete
   - **Receiver**: `wani-client receive [-out <dir>] <code>`; show manifest after pairing (file count + total size); progress bar; summary on complete
   - Progress bar format: `[####----] 45% | 450MB/1GB | file 3/7 | eta 12s` (use `time.Since` for ETA; simple terminal width detection via `os.Stdout.Stat()`)

2. Receiver: if `-out` dir doesn't exist, create it with `os.MkdirAll`

**Verification**: Full Phase 3 verification from ROADMAP — directory of mixed files between two machines on different networks; xxHash verified; resume after mid-transfer kill.

---

## Relevant Files

| File | Change |
|---|---|
| `internal/protocol/messages.go` | Add `MsgICECredentials` |
| `internal/protocol/protocol.go` | Add `FileEntry`, `Manifest`, `ManifestResponse` |
| `internal/client/signaling.go` | Add `SendICECredentials`, `ReadICECredentials` |
| `internal/client/ice.go` | **New**: `NewICEAgent`, `GatherAndConnect` |
| `internal/client/quic.go` | **New**: `icePacketConn` adapter, `DialQUIC`, `ListenQUIC` |
| `internal/client/manifest.go` | **New**: `BuildManifest` |
| `internal/client/transfer.go` | **New**: `SendManifest`, `ReceiveManifest`, `SendFiles`, `ReceiveFiles` |
| `internal/client/resume.go` | **New** (3d): `ResumeState`, `LoadResume`, `SaveResume` |
| `cmd/wani-client/main.go` | Incremental updates each phase; flags + wiring |
| `go.mod` / `go.sum` | Add `pion/ice/v4`, `quic-go/quic-go`, `cespare/xxhash/v2` |

## Scope Exclusions
- No TURN/relay (Phase 4)
- No concurrent multi-file streaming (sequential streams)
- No compression (Compression field set to "none" per architecture 8a)
- No per-chunk resume (per-file only, per architecture 8b)
- No terminal UI library — plain `fmt.Printf` for progress bar
