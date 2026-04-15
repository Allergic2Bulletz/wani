---
description: "Use when implementing the minimal health check server. Covers HTTP server, /health endpoint, wani-server entrypoint wiring."
---
# Health Check Server — Implementation Spec

## Relevant Architecture Decisions

- **Decision 9 (Single Binary):** wani-server is one binary serving signaling, STUN, and (later) TURN. The health endpoint is the first HTTP handler on this binary.
- **Decision 10 (Core Lib + CLI):** Server logic lives in `internal/server/`. `cmd/wani-server/main.go` is the entrypoint — it parses flags, creates the server, and calls `ListenAndServe`. No business logic in `cmd/`.
- **Decision 13 (VM + CI/CD):** The health endpoint is the verification target for both manual SCP deploy and the GitHub Actions pipeline. Must respond on the port the VM firewall exposes.

## Libraries & APIs

- `net/http` — stdlib only. No framework needed for a single route.
- No external dependencies for this feature.

## Files to Create or Modify

| File | Action | Purpose |
|------|--------|---------|
| `internal/server/server.go` | Modify | `Server` struct, `New()` constructor, `ListenAndServe()`, health handler |
| `cmd/wani-server/main.go` | Modify | Parse `--addr` flag, create `server.Server`, call `ListenAndServe` |

## Protocol / Data Flow

```
Client                          wani-server
  |                                 |
  |  GET /health HTTP/1.1           |
  |  ──────────────────────────►    |
  |                                 |
  |  HTTP/1.1 200 OK               |
  |  Content-Type: text/plain       |
  |  Body: "ok"                     |
  |  ◄──────────────────────────    |
```

The server listens on a configurable address (default `:8080`). The `/health` route is the only registered handler for now. All other paths return 404.

## Implementation Notes

- `Server` struct holds the `*http.ServeMux` and listen address. This struct will grow to hold WebSocket upgrader, session store, etc. in Phase 2.
- `New(addr string) *Server` — constructor. Registers `/health` on `ServeMux`.
- `ListenAndServe() error` — wraps `http.ListenAndServe`.
- Health handler writes `"ok"` with status 200.  No JSON, no version info — keep it trivial.
- `cmd/wani-server/main.go` uses `flag.StringVar` for `--addr`, defaults to `:8080`.

## Acceptance Criteria

- `go build ./cmd/wani-server` produces a binary.
- `./wani-server` starts and listens on `:8080` by default.
- `./wani-server --addr :9090` listens on port 9090.
- `curl http://localhost:8080/health` returns HTTP 200 with body `ok`.
- `curl http://localhost:8080/nonexistent` returns HTTP 404.
- Server logs the listen address to stderr on startup.
- Ctrl-C cleanly stops the server (no goroutine leaks — but no graceful shutdown plumbing needed yet).

## Roadmap Reference

Phase 0, task 3: "Minimal wani-server: HTTP health check endpoint (`GET /health` → 200 OK)"

## Open Questions

None — this is fully specified by existing architecture decisions.
