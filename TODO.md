## TODOs

- [ ] Prompt before overwriting existing files on receive (ask user yes/no per file, overwrite all, or add `--overwrite` flag)
- [ ] Investigate cross-platform CLI argument parsing — currently there is ad-hoc handling (e.g. stripping trailing `"` characters from pairing codes) but there may be more edge cases across platforms (Windows shell quoting, PowerShell, etc.). Centralize all argument sanitization in one place in `cmd/wani-client/`.

## Future Considerations

### UDP Buffer Sizing (quic-go + pion/ice Architecture Limitation)

**Problem:** quic-go logs a warning at startup — "connection doesn't allow setting of receive buffer size. Not a *net.UDPConn?" — because our P2P data path routes through `icePacketConn`, an adapter that wraps `*ice.Conn` (pion's virtual stream) rather than exposing a real OS UDP socket. quic-go tries to enlarge the OS receive buffer from ~200KB to 7MB for throughput, but can't reach the underlying socket through the adapter. The buffer stays at the OS default, which can cause packet drops at high sustained throughput (roughly 1 Gbps+ or high-latency paths).

**Potential fix — ICE for hole-punching only, raw socket for data:**
After ICE selects a winning candidate pair, we know the exact local and remote UDP address. Instead of routing data through pion's internal pipe, we could open a fresh `*net.UDPConn` bound to the same local port using `SO_REUSEPORT` and hand that directly to quic-go. quic-go gets a real OS socket it can tune. The complication: pion/ice runs STUN keepalives on that port to maintain the NAT hole — these would need to be demultiplexed away from QUIC data. This is a non-trivial architectural change, worth revisiting as a performance pass once relay and resume are complete.

**Why adding TURN relay (Phase 4) does NOT fix this:**
ICE's built-in relay candidates (TURN) still route data through `*ice.Conn` and the same `icePacketConn` adapter. The warning would persist. The buffer limitation would only go away on the relay path if relay is implemented as a *separate* non-ICE code path — a direct `*net.UDPConn` to the TURN server handed straight to quic-go, bypassing pion/ice for the data plane entirely. This is a valid long-term design (ICE for P2P discovery, raw UDP for relay data) but means two distinct relay code paths rather than reusing ICE's built-in relay support.