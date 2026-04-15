# Plan: Centralized CLI Argument Sanitization + Receive Fix

## Context

**Bug:** `receive -out '.\Bokida: Heartfelt Reunion (Soundtrack)' chamber-backstab-curfew-army` → "missing pairing code"

**Root cause:** Windows PowerShell 5.1 Legacy mode fails to pass the path `'.\Bokida: Heartfelt Reunion (Soundtrack)'` (contains `(...)` and `:`) cleanly to the native executable. The path argument is dropped entirely, leaving only `["-out", "chamber-backstab-curfew-army"]` reaching Go. `flag.FlagSet` then consumes the code as the `-out` value. `fs.NArg() == 0` → "missing pairing code".

**Secondary issue:** If PS5.1 passes a path without quoting (splits at spaces), the code lands at NArg > 1.

**Related TODO:** Centralize all argument sanitization in `cmd/wani-client/`.

---

## Phase 1 — Diagnostic

1. In `run()`, before subcommand dispatch, when `-debug` is set:
   ```go
   fmt.Fprintf(os.Stderr, "debug: os.Args = %v\n", os.Args)
   ```
   This confirms exactly what PS5.1 is passing to the binary.

---

## Phase 2 — Centralize sanitization

2. Create `cmd/wani-client/args.go` with two helpers:
   - `sanitizePath(s string) string` → `filepath.Clean(strings.TrimSpace(strings.Trim(s, "'\"")))`
   - `sanitizeCode(s string) string` → `strings.ToLower(strings.TrimSpace(strings.Trim(s, "'\"")))`

   Both strip leading/trailing single and double quotes and whitespace. `sanitizePath` additionally calls `filepath.Clean`. All stdlib — no new dependencies.

3. In `runSend`: replace ad-hoc `filepath.Clean(strings.TrimRight(fs.Arg(0), `"`))` with `sanitizePath(fs.Arg(0))`.

4. In `runReceive`: replace ad-hoc `filepath.Clean(strings.TrimRight(*outDir, `"`))` with `sanitizePath(*outDir)` (superseded by Phase 3 refactor).

---

## Phase 3 — Fix receive arg parsing

5. Add `internal/protocol` to imports in `main.go`.

6. Add `parseReceiveArgs(args []string) (outDir, code string, err error)` to `args.go`:
   - Use `flag.FlagSet` to parse `-out` and positional args normally.
   - **NArg == 0 recovery:** If `NArg == 0` AND `protocol.ValidateCode(sanitizeCode(*outDir))` is true → the code was consumed as the `-out` value (path dropped by PS5.1). Set `code = sanitizeCode(*outDir)`, `outDir = "."`. Print a warning to stderr pointing at `-out=<dir>` syntax.
   - **NArg == 1:** `code = sanitizeCode(fs.Arg(0))` — the normal case.
   - **NArg > 1:** Return error: "unexpected extra arguments — is the output path properly quoted?"
   - Always apply `sanitizePath(*outDir)` before returning.

7. Replace inline `flag.FlagSet` boilerplate + manual code extraction in `runReceive` with a call to `parseReceiveArgs(args)`.

---

## Relevant Files

| File | Change |
|---|---|
| `cmd/wani-client/main.go` | `run()` debug dump; `runSend` sanitizePath; `runReceive` call parseReceiveArgs; add `internal/protocol` import |
| `cmd/wani-client/args.go` | **NEW** — `sanitizePath`, `sanitizeCode`, `parseReceiveArgs` |
| `internal/protocol/codes.go` | Read-only — `ValidateCode` used by `parseReceiveArgs` |

---

## Verification

1. `./build.sh` — no compile errors.
2. **Simulate PS5.1 drop:** `./wani-client receive -out chamber-backstab-curfew-army` → warns "output path looks like a pairing code" and attempts connect with the code (no "missing pairing code" error).
3. **Normal case:** `./wani-client receive -out /tmp/out chamber-backstab-curfew-army` → parses correctly.
4. **Code-only:** `./wani-client receive chamber-backstab-curfew-army` → uses default `outDir = "."`.
5. **Trailing-quote artifact:** `./wani-client receive -out './some/path"' code` → `sanitizePath` strips the rogue `"`.
6. **Send path:** `./wani-client send "./some path"` → `sanitizePath` cleans quoting artifacts.
7. **Debug mode:** `./wani-client -debug receive -out mydir code` → prints `os.Args` to stderr.

---

## Decisions

- `parseReceiveArgs` imports `internal/protocol` for `ValidateCode` — consistent with existing `internal/client` and `internal/identity` imports.
- `ValidateCode` does not enforce a 4-word count; it only checks that all hyphen-separated parts are in the wordlist. A false positive requires a directory name that is coincidentally N wordlist words joined by hyphens — negligible in practice.
- The `-out=<dir>` form (value joined with `=`) is immune to PS5.1 separate-token quoting failures and should be documented as the preferred usage in the help string.
- Scope: `runSend` path sanitization updated; `runReceive` fully refactored; no other files changed.

---

## Further Considerations

- **`-out=<dir>` in usage string:** Even without any code fix, documenting `-out=<dir>` as the canonical form gives users a reliable workaround immediately.
- **NArg > 1 path-splitting recovery:** An alternative to erroring is to treat the last positional arg as the code if it passes `ValidateCode`. This recovers when PS5.1 splits a space-containing path across multiple args. Tradeoff: silently ignores stray args. Could be a follow-up.
