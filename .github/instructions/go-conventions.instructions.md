---
description: "Go coding conventions for the Wani project. Error handling, package layout, naming."
applyTo: "**/*.go"
---
# Go Conventions

## Package Layout

- `cmd/wani-server/` and `cmd/wani-client/` — entrypoints only. Parse flags, wire dependencies, call into `internal/`.
- `internal/` — all business logic. Enforces that external packages cannot import wani internals.
- Core library packages (`internal/client/`, `internal/protocol/`, `internal/identity/`) must have **zero** terminal I/O. No `fmt.Println`, no `os.Stdin`, no `log.Fatal`. Return errors instead.
- Only `cmd/` packages may print to stdout/stderr or call `os.Exit`.

## Error Handling

- Always return errors; never panic except for programmer bugs (unreachable states).
- Wrap errors with context: `fmt.Errorf("server.Listen: %w", err)`
- Use `errors.Is` / `errors.As` for checking, not string comparison.

## Naming

- Exported types and functions use PascalCase. Unexported use camelCase.
- Interface names: `Identity`, `Transport` — not `IIdentity` or `TransportInterface`.
- Package names are short, lowercase, singular: `protocol`, `identity`, `server`.

## Dependencies

- Prefer stdlib where sufficient. Key external deps:
  - `github.com/quic-go/quic-go` — QUIC transport
  - `github.com/pion/ice/v4` — ICE agent
  - `github.com/pion/turn/v4` — TURN server
  - `github.com/cespare/xxhash/v2` — file integrity hashing
  - WebSocket library (gorilla/websocket or nhooyr.io/websocket)
