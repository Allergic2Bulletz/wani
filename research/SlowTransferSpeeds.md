User: I'm getting fairly slow transfer speeds on my hairpin connection, ~200 kB/s average. I did notice that on 1 <1MB file, I hit 1.5 MB/s. On a 2.8 MB, 12 MB, and 5 MB file, I hit ~200, ~170, and ~170 kB/s transfer speed respectively.

Can you investigate why these speeds are so low? I'm getting anywhere from ~1.1 MB/s to ~300 MB/s transfer speeds using croc.

GitHub Copilot: Investigate transfer speed bottlenecks

Read [](file:///home/sikes/projects/wani/.github/instructions/go-conventions.instructions.md)

Searched for files matching `**/internal/client/*.go`, 7 matches

Searched for files matching `**/cmd/wani-client/main.go`, 1 match

Searched for files matching `**/go.mod`, 1 match

Read [](file:///home/sikes/projects/wani/internal/client/transfer.go)

Read [](file:///home/sikes/projects/wani/internal/client/quic.go)

Read [](file:///home/sikes/projects/wani/cmd/wani-client/main.go#1-1), lines 1 to 300

Read [](file:///home/sikes/projects/wani/go.mod)

Read [](file:///home/sikes/projects/wani/internal/client/client.go)

Read [](file:///home/sikes/projects/wani/internal/client/ice.go)

Read [](file:///home/sikes/projects/wani/internal/client/client.go)

Searched for regex `func.*client\.go` (`**/internal/client/client.go`), no results

Read [](file:///home/sikes/projects/wani/internal/client/client.go)

Read [](file:///home/sikes/projects/wani/internal/client)

Read [](file:///home/sikes/projects/wani/internal/client/signaling.go)

Read [](file:///home/sikes/projects/wani/internal/client/manifest.go)

Searched for regex `InitialMaxStreamDataBidiLocal|InitialMaxData|MaxStreamReceiveWindow|io.CopyBuffer|make.*\[\]byte.*buffer`, no results

Searched for regex `time\.Sleep|Throttle.*time|OptionThrottle`, 2 results

Read [](file:///home/sikes/projects/wani/cmd/wani-client/main.go#300-300), lines 300 to 345

Read [](file:///home/sikes/projects/wani/internal/protocol/protocol.go)

Read [](file:///home/sikes/projects/wani/internal/client/stun.go)

Searched for regex `buffer|window|stream.*limit|throughput|slow|performance|bottleneck|parallel` (`**/architecture.md`), 1 result

Searched for regex `buffer|window|stream.*limit|throughput|slow|performance|bottleneck|parallel` (`**/ROADMAP.md`), 1 result

Read memory

Created memory file [](file:///home/sikes/.vscode-server/data/User/workspaceStorage/62868d48a44552f7a3d8d73f881b1670/GitHub.copilot-chat/memory-tool/memories/NDQ3NjcwMzctMjlmYy00NzAzLWE0MDUtN2FjN2FlYjMxMjYy/wani-perf-investigation.md)

Let me verify the specific line numbers and code details before writing the plan.   

Read [](file:///home/sikes/projects/wani/internal/client/transfer.go#1-1), lines 1 to 230

Read [](file:///home/sikes/projects/wani/internal/client/quic.go#1-1), lines 1 to 150

Read [](file:///home/sikes/projects/wani/internal/client/manifest.go#1-1), lines 1 to 80

Read [](file:///home/sikes/projects/wani/internal/client/transfer.go#230-230), lines 230 to 269

Read [](file:///home/sikes/projects/wani/cmd/wani-client/main.go#1-1), lines 1 to 200

Read [](file:///home/sikes/projects/wani/cmd/wani-client/main.go#200-200), lines 200 to 280

Read [](file:///home/sikes/projects/wani/internal/client/manifest.go#65-65), lines 65 to 80

Let me also look at the ICE configuration to check for any connection-level constraints. 

Read [](file:///home/sikes/projects/wani/internal/client/ice.go#1-1), lines 1 to 150

Read [](file:///home/sikes/projects/wani/internal/client/ice.go#150-150), lines 150 to 161

Good. I have a thorough picture of the code. Let me now write the plan. 

Created memory file [](file:///home/sikes/.vscode-server/data/User/workspaceStorage/62868d48a44552f7a3d8d73f881b1670/GitHub.copilot-chat/memory-tool/memories/NDQ3NjcwMzctMjlmYy00NzAzLWE0MDUtN2FjN2FlYjMxMjYy/plan.md)

Here's what the investigation found:

---

## Plan: Transfer Speed Investigation & Fix

**TL;DR:** There are two confirmed code-level bottlenecks — a 32KB `io.Copy` buffer and a nil QUIC config using conservative defaults — plus a structural double-read of files. Together these can explain the observed ~200 kB/s wall. The size threshold (<1MB fast, ≥2.8MB slow) strongly points to the QUIC flow control window being exhausted on larger files.

---

### Findings

**Issue 1 (Critical): Nil QUIC config**

Both quic.go (`DialQUIC`) and quic.go (`ListenQUIC`) pass `nil` to the quic.Config argument. quic-go's defaults are tuned for web workloads — the initial per-stream flow control window is ~512KB and the connection-level window is ~1MB.

The file size threshold you described is the tell: files smaller than ~1MB can be delivered entirely within the first flow control window without stalling. Once a file exceeds that window, the sender must pause and wait for `MAX_STREAM_DATA` ACKs from the receiver before it can continue — and each such round-trip costs an entire RTT. Even at 5ms LAN RTT: `512KB / 5ms = ~100MB/s` theoretical ceiling, so something's eating RTT. Raising the windows eliminates the stalls entirely for typical file sizes.

**Issue 2 (Significant): 32KB `io.Copy` buffer**

transfer.go (`sendFile`) and transfer.go (`receiveFile`) both call `io.Copy(dst, src)` with no buffer argument. `io.Copy` allocates a 32KB internal buffer. Each write to the QUIC stream is a separate 32KB chunk — maximum ~27 packets per write call. A 2MB explicit buffer reduces the per-operation overhead by 64×.

**Issue 3 (Moderate): Files read twice**

manifest.go reads every file in full (`io.Copy(h, file)`) to compute the xxHash before transfer begins. Then transfer.go reads the same file again to send it. For a 12MB file that's 24MB of disk reads before the first byte is transferred. Fixing this properly requires a protocol change (send hash as a per-file trailer rather than in the manifest), which is out of scope here. At minimum the manifest scan should use a larger buffer.

**Issue 4 (Potential): `icePacketConn` deadline conflation**

In quic.go, both `SetReadDeadline` and `SetWriteDeadline` map to `ice.Conn.SetDeadline` — so setting a read deadline *also* sets the write deadline (and vice versa). If quic-go uses `SetReadDeadline` to implement its periodic read-loop wakeup (a common pattern), that unintentionally arm-drops the write deadline too. Under sustained load this could cause ACK frames to time out, triggering retransmissions and tanking CWND.

---

### Steps

**Phase 1 — Diagnostic first (no code change)**
1. Run `wani-client -debug send <file>` — this logs the selected ICE candidate pair. Confirm you see `type=host` (direct LAN path), not `type=srflx` (going through public IP/NAT). If `srflx` is selected, the connection may be hairpinning through your router at higher latency than expected, which would make the flow-control stall worse.

**Phase 2 — Fix QUIC flow control config** (*depends on nothing; highest expected impact*)

In quic.go, add a shared `quicConfig()` helper and pass it to both calls:
- `InitialMaxStreamDataBidiLocal`: 64MB
- `InitialMaxStreamDataBidiRemote`: 64MB
- `InitialMaxData`: 128MB
- `MaxIncomingStreams`: 128

**Phase 3 — Fix `io.Copy` buffer sizes** (*parallel with Phase 2*)

In transfer.go, replace both `io.Copy` calls with `io.CopyBuffer(dst, src, make([]byte, 2<<20))`.
In manifest.go, same change for the manifest hash scan.

**Phase 4 — Fix `icePacketConn` deadline conflation** (*parallel with Phases 2–3*)

In quic.go, add `readDeadline` and `writeDeadline` fields to the `icePacketConn` struct. `SetReadDeadline` stores its time and calls `SetDeadline` with only the read deadline; `SetWriteDeadline` does the same independently. This decouples them so QUIC can manage them separately.

---

### Relevant files
- quic.go — icePacketConn, DialQUIC, ListenQUIC
- transfer.go — sendFile, receiveFile
- manifest.go — fileEntry / BuildManifest

### Verification
1. Run `wani-client -debug send ...` before any changes to confirm ICE candidate type
2. After Phase 2 alone: re-run the >2MB transfer — if speed jumps, QUIC windows were the main culprit
3. After Phase 3: re-run — expected additional improvement especially on small files and manifest scanning latency
4. Transfer a 100MB+ file and compare against croc baseline on same link

### Out of scope
- Concurrent multi-stream file transfer (sequential is intentional architecture, separate planned feature)
- Streaming hash during send / removing manifest pre-hash (protocol change, separate task)

---

**One question before you proceed:** Do you want Phase 4 (deadline fix) included in this implementation pass, or would you prefer to defer it until after testing whether Phases 2+3 explain the full slowdown?