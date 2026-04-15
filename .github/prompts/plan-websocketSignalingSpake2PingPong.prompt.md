# Plan: Phase 2a-2d — WebSocket Signaling + SPAKE2 Ping-Pong

## TL;DR
Build out the complete signaling + key exchange stack in four sub-phases ending in a demo-able ping-pong milestone. Server gains a WebSocket endpoint with session management; client gains pairing code generation, SPAKE2 key exchange, and authenticated ping-pong. Libraries: `gorilla/websocket`, `bytemare/spake2`.

---

## Phase A — Dependencies + Protocol Types (no blockers, start here)

**Step 1** — Add dependencies to go.mod  
`go get github.com/gorilla/websocket github.com/bytemare/spake2`

**Step 2 (parallel with Step 1)** — Define wire protocol in `internal/protocol/messages.go`  
Define `SignalMessage` struct with JSON tags and string type constants:
- Client→Server: `create_session`, `join_session` (+ Code field), `relay` (+ Payload []byte base64)
- Server→Client: `session_created` (+ Code field), `session_ready`, `relay`, `error` (+ Message field)

---

## Phase B — Pairing Codes + Session Store (parallel, depends on Step 2)

**Step 3** — Pairing code generator in `internal/protocol/codes.go`  
- Source 2048-word curated wordlist → save as `internal/protocol/wordlist.txt`  
  (Source: EFF large wordlist filtered to 2048 entries, or magic-wormhole wordlist)
- Embed with `//go:embed wordlist.txt` into a `[]string` variable  
- `GenerateCode(n int) (string, error)` — uses `crypto/rand` to pick n indices, joins with `-`
- `ValidateCode(code string) bool` — verifies all words are in the set, correct format

**Step 4 (parallel with Step 3)** — Session store in `internal/server/sessions.go`  
- `Session` struct: `senderConn`, `receiverConn` (*websocket.Conn), `expiresAt` time.Time, `writeMu sync.Mutex` per connection  
- `SessionStore` struct: `mu sync.RWMutex`, `sessions map[string]*Session`
- Methods: `Create(code string) *Session`, `Get(code string) (*Session, bool)`, `Delete(code string)`
- `StartCleanup(ctx context.Context)` — goroutine that ticks every 1 min, deletes expired sessions (5 min TTL)

---

## Phase C — WebSocket Server Handler + EphemeralIdentity (depends on B)

**Step 5** — WebSocket endpoint in `internal/server/ws.go`  
- `gorilla/websocket.Upgrader` with `CheckOrigin` (allow all for MVP)
- `handleWS(w, r)`: upgrades connection → starts read loop → dispatches on message type
- `handleCreateSession`: calls protocol.GenerateCode(4) → store.Create → writes session_created back
- `handleJoinSession`: looks up code → stores receiver conn → writes session_ready to **both** sender and receiver (synchronize each conn write with the conn's mutex in Session)
- `handleRelay`: looks up session by conn (maintain `connToCode map[*websocket.Conn]string`) → forwards payload to the other peer
- On any disconnect: clean up session, close both conns if connected

**Step 6 (register route)** — Modify `internal/server/server.go`  
- Add `upgrader gorilla/websocket.Upgrader` and `*SessionStore` fields to `Server`  
- Register `mux.HandleFunc("/ws", s.handleWS)` in `New()`  
- Call `store.StartCleanup(ctx)` (expose a `Start(ctx)` method or pass ctx to `New`)

**Step 7 (parallel with Step 5/6)** — `EphemeralIdentity` in `internal/identity/ephemeral.go`  
- Sender = SPAKE2 Party A, Receiver = SPAKE2 Party B (hardcode roles by convention)
- `type Role int` with `RoleSender`, `RoleReceiver` constants
- `EphemeralIdentity` struct wraps `bytemare/spake2` client state + stores computed `secret []byte`
- `NewEphemeralIdentity(role Role, password []byte) (*EphemeralIdentity, error)`
- `Message() ([]byte, error)` — returns SPAKE2 message to send to peer (call once)
- `Finish(peerMessage []byte) error` — processes peer's message, stores shared secret
- `SharedSecret() []byte` — implements `Identity` interface; panics if Finish not yet called

---

## Phase D — Client Signaling + SPAKE2 Flow + CLI (depends on C)

**Step 8** — Signaling client in `internal/client/signaling.go`  
Zero terminal I/O — returns errors and data only.  
- `SignalingClient` struct: `conn *websocket.Conn`  
- `Connect(ctx context.Context, serverURL string) (*SignalingClient, error)`  
- `CreateSession() (code string, err error)` — sends create_session, reads until session_created  
- `JoinSession(code string) error` — sends join_session, reads until session_ready or error  
- `ExchangeSPAKE2(id *identity.EphemeralIdentity) error`:  
  1. Calls `id.Message()` → sends as relay payload  
  2. Reads next relay message from conn  
  3. Calls `id.Finish(payload)` → shared secret stored on identity  
- `PingPong(id *identity.EphemeralIdentity) error` (sender side):  
  1. Compute `HMAC-SHA256(id.SharedSecret(), []byte("wani-ping"))` → send as relay  
  2. Read next relay message → verify it equals `HMAC-SHA256(id.SharedSecret(), []byte("wani-pong"))`  
  3. Return nil on match, error on mismatch  
- `WaitAndPong(id *identity.EphemeralIdentity) error` (receiver side):  
  1. Read next relay message → verify it equals expected ping HMAC  
  2. Compute pong HMAC → send as relay  
  3. Return nil on success

**Step 9** — CLI wiring in `cmd/wani-client/main.go`  
- Usage: `commands: stun, send, receive`  
- `runSend(serverURL string)`:  
  1. `SignalingClient.Connect` → `CreateSession` → print code  
  2. `NewEphemeralIdentity(RoleSender, []byte(code))` → `ExchangeSPAKE2` → `PingPong`  
  3. If PingPong succeeds: print "Paired successfully!"  
- `runReceive(code string, serverURL string)`:  
  1. `SignalingClient.Connect` → `JoinSession(code)`  
  2. `NewEphemeralIdentity(RoleReceiver, []byte(code))` → `ExchangeSPAKE2` → `WaitAndPong`  
  3. If WaitAndPong succeeds: print "Paired successfully!"  
- Add `-server` flag (default: `ws://localhost:8080/ws`) to both send/receive

---

## Relevant Files

- `go.mod` — add gorilla/websocket + bytemare/spake2
- `internal/protocol/messages.go` — **create**: SignalMessage, type constants
- `internal/protocol/codes.go` — **create**: GenerateCode, ValidateCode
- `internal/protocol/wordlist.txt` — **create**: 2048-word embedded wordlist
- `internal/server/sessions.go` — **create**: Session, SessionStore, StartCleanup
- `internal/server/ws.go` — **create**: WebSocket upgrade + dispatch + relay
- `internal/server/server.go` — **modify**: add upgrader + session store fields, register /ws, call StartCleanup
- `internal/identity/identity.go` — **keep as-is** (SharedSecret() sufficient for Phase 2)
- `internal/identity/ephemeral.go` — **create**: EphemeralIdentity with SPAKE2
- `internal/client/signaling.go` — **create**: SignalingClient, Pair flow
- `cmd/wani-client/main.go` — **modify**: add send + receive commands, -server flag

---

## Verification

1. `go build ./...` — passes with new deps
2. `go vet ./...` — no issues
3. In terminal A: `wani-server -addr :8080` — server starts
4. In terminal B: `wani-client send -server ws://localhost:8080/ws` → prints 4-word code
5. In terminal C: `wani-client receive <code> -server ws://localhost:8080/ws` → both terminals print "Paired successfully!" within ~1s
6. Wrong code test: `wani-client receive wrong-code ...` → prints "Pairing failed" (or server error)
7. Expired session test: start send, wait 5+ min, then try receive → error: session expired

---

## Decisions

- Sender = SPAKE2 Party A; Receiver = SPAKE2 Party B (roles must match between peers)
- Relay payload is raw bytes base64-encoded in JSON (gorilla handles []byte as base64 automatically)
- Write lock per connection (not per session) to satisfy gorilla's single-writer constraint
- connToCode reverse map for O(1) relay routing
- `WaitAndPong` and `PingPong` use the relay message channel (no new message type needed)
- Scope: Phase 2a–2d only; ICE candidate exchange (2e) and file transfer (Phase 3) excluded

## Open Questions

- Wordlist source: EFF large wordlist subset vs magic-wormhole wordlist (either works; implementer decides)
- bytemare/spake2 exact API: read package docs before implementing Step 7
- Server needs context plumbing for StartCleanup — decide in Step 6 whether to thread ctx through New() or add a Start(ctx) method
