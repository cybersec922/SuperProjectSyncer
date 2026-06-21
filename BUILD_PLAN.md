# SuperProjectSyncer — Build Plan

Canonical build and architecture record. Edit this file when changing language, dependencies, or major design decisions.

## Language

| Field | Value |
|-------|-------|
| **Current** | Go 1.22+ |
| **Module** | `github.com/superdata/superprojectsyncer` |
| **Binary** | `sps` |

## Why Go

- Single static binary for Windows and Linux
- Strong cross-platform service support (`golang.org/x/sys/windows/svc`, systemd)
- Mature ecosystem for file watching, mDNS, TLS, SQLite
- Simple cross-compilation

## Alternatives considered

| Language | Pros | Cons |
|----------|------|------|
| **Rust** | Performance, safety | Slower iteration, heavier build |
| **C#** | Great Windows services | Linux story weaker without .NET runtime |
| **Python** | Fast to prototype | Requires runtime on each machine, weaker for long-running daemons |

## Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/BurntSushi/toml` | Config parsing |
| `github.com/fsnotify/fsnotify` | File system watching |
| `github.com/grandcat/zeroconf` | mDNS discovery |
| `modernc.org/sqlite` | Pure-Go SQLite (no CGO) |
| `golang.org/x/sys` | Windows service support |
| `lukechampine.com/blake3` | Fast file hashing |

## Build commands

```bash
# Local build
go build -o bin/sps ./cmd/sps

# Windows (from any host with Go)
GOOS=windows GOARCH=amd64 go build -o bin/sps.exe ./cmd/sps

# Linux
GOOS=linux GOARCH=amd64 go build -o bin/sps-linux ./cmd/sps
```

## Project layout

```
SuperProjectSyncer/
├── BUILD_PLAN.md          # this file
├── cmd/sps/main.go        # CLI entrypoint
├── internal/
│   ├── app/               # orchestrates sync groups
│   ├── approval/          # ask_folder queue
│   ├── config/            # TOML load + validation
│   ├── discovery/         # mDNS advertise/browse
│   ├── ignore/            # glob matcher
│   ├── protocol/          # wire messages
│   ├── state/             # SQLite metadata
│   ├── sync/              # sync engine
│   ├── transport/         # TLS listener/dial
│   ├── watcher/           # fsnotify debounce
│   └── service/           # install/uninstall
├── config.example.toml
├── go.mod
└── README.md
```

## Architecture (language-agnostic)

### Config schema

See `config.example.toml`. Each `[[sync]]` block defines a named sync group. Peers match by `name` and `sync_key`.

### Wire protocol (v1)

Binary-framed messages over TLS 1.3. Certs derived from `sync_key` (no external CA).

| Type | Code | Payload |
|------|------|---------|
| HELLO | 1 | sync name, peer ID, direction, role, version |
| MANIFEST | 2 | JSON array of {path, hash, size, mtime} |
| NEED | 3 | JSON array of paths |
| CHUNK | 4 | path + offset + data |
| APPLY_OK | 5 | path |
| REJECT | 6 | folder path + reason |
| PING | 7 | empty |

Frame format: `4-byte big-endian length` + `1-byte type` + payload.

### Sync behavior

1. Watch `local_path`, debounce per top-level folder (500ms)
2. Build folder batch; if `approval = ask_folder`, queue until approved
3. Exchange manifests; transfer only changed files (BLAKE3 hash)
4. Apply via temp file + rename; conflicts → `path.conflict.<timestamp>`
5. Persist state in SQLite under `data_dir`

### Direction rules

| direction | role | Behavior |
|-----------|------|----------|
| bidirectional | any | Both sides send and receive |
| push | provider | Sends changes only |
| push | consumer | Receives only |
| pull | consumer | Initiates fetch from providers |
| pull | provider | Responds to fetch requests |

## Changing language

Keep unchanged:
- `config.example.toml` schema
- Wire protocol message types and frame format
- Sync behavior spec above
- `BUILD_PLAN.md` architecture section

Rewrite:
- All of `internal/` and `cmd/`
- `go.mod` → new dependency manifest
- Build commands section in this file

## Implementation phases

- [x] Phase 1 — BUILD_PLAN.md, config, ignore, watcher, protocol, transport, basic sync, CLI
- [x] Phase 2 — direction/role, ask_folder, mDNS, multi-sync
- [x] Phase 3 — service install, SQLite state, README
- [ ] Phase 4 — web UI, .syncignore, dry-run (optional, future)

## Changelog

| Date | Change |
|------|--------|
| 2026-06-21 | Initial Go implementation per design plan |
