# SuperProjectSyncer — Build Plan

Canonical build and architecture record. Edit this file when changing language, dependencies, or major design decisions.

## Language

| Field | Value |
|-------|-------|
| **Current** | Go 1.22+ |
| **Module** | `github.com/superdata/superprojectsyncer` |
| **Binary** | `sps` (sync daemon + relay server in one binary) |

## Why Go

- Single static binary for Windows and Linux
- Strong cross-platform service support (`golang.org/x/sys/windows/svc`, systemd)
- Mature ecosystem for file watching, mDNS, TLS, SQLite
- Simple cross-compilation; GitHub Actions builds release artifacts

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
| `github.com/google/uuid` | Peer IDs |

## Build commands

```bash
# Local build
go build -o bin/sps ./cmd/sps

# Windows (from any host with Go)
GOOS=windows GOARCH=amd64 go build -o bin/sps.exe ./cmd/sps

# Linux amd64 / arm64
GOOS=linux GOARCH=amd64 go build -o bin/sps-linux ./cmd/sps
GOOS=linux GOARCH=arm64 go build -o bin/sps-linux-arm64 ./cmd/sps
```

### GitHub Releases

Push a version tag to build and publish binaries:

```bash
git tag v0.2.0
git push origin v0.2.0
```

Workflow: `.github/workflows/release.yml` — produces `sps-windows-amd64.exe`, `sps-linux-amd64`, `sps-linux-arm64`, and `SHA256SUMS.txt`.

## Project layout

```
SuperProjectSyncer/
├── BUILD_PLAN.md
├── README.md
├── config.example.toml      # client sync daemon config
├── relay.example.toml       # relay server config (VPS hub)
├── cmd/sps/main.go          # CLI: run, install, status, watch, logs, relay
├── internal/
│   ├── app/                 # orchestrates sync groups, relay client, status UI
│   ├── approval/            # ask_folder queue
│   ├── config/              # TOML load + validation (client + relay server)
│   ├── discovery/           # mDNS advertise/browse
│   ├── ignore/              # glob matcher
│   ├── logging/             # file log setup + tail
│   ├── protocol/            # sync wire messages
│   ├── relay/               # relay server + NAT client virtual conns
│   ├── state/               # SQLite metadata + activity / transfer jobs
│   ├── sync/                # sync engine
│   ├── transport/           # TLS listener/dial
│   ├── watcher/             # fsnotify debounce
│   └── service/             # Windows service + Linux systemd install
├── configs/                 # gitignored — local machine configs
├── .github/workflows/       # release CI
└── go.mod
```

## Architecture (language-agnostic)

### Config schema

**Client** — see `config.example.toml`. Each `[[sync]]` block defines a named sync group. Peers match by `name` and `sync_key`.

| `global` field | Purpose |
|----------------|---------|
| `listen` | TCP listen for direct P2P (default `0.0.0.0:7741`) |
| `data_dir` | SQLite state + default log path |
| `discovery` | mDNS on LAN |
| `log_file` | Log path; empty = `<data_dir>/sps.log`; `none` = stderr only |
| `relay` | Optional relay server `host:port` (NAT hub) |
| `relay_key` | Auth secret for relay (required if `relay` set) |

**Relay server** — see `relay.example.toml`:

| field | Purpose |
|-------|---------|
| `listen` | Default `0.0.0.0:7750` |
| `key` | Shared secret; clients use same value as `relay_key` |
| `log_file` | Optional relay log path |

Local configs live in `configs/` (gitignored).

### Connectivity modes

| Mode | Config | When |
|------|--------|------|
| **Direct** | `peers = ["public-ip:7741"]` | One side has public IP or port forward |
| **LAN** | `discovery = true`, same `name` | Same network |
| **Relay** | `relay = "vps:7750"`, `relay_key` | Both peers behind NAT; outbound-only |
| **Hybrid** | `peers` + `relay` | Direct when reachable, relay as hub |

**NAT rule:** private machines dial public ones. For push-only sync, only the **receiver** needs inbound `7741`. Relay uses outbound `7750` from both peers to the VPS.

### Wire protocol (v1) — sync

Binary-framed messages over TLS 1.3. Self-signed certs per connection; `sync_key` validated in HELLO.

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

### Wire protocol — relay (control plane)

Separate message types on the relay TCP connection (`internal/relay/protocol.go`):

| Type | Code | Purpose |
|------|------|---------|
| AUTH | 20 | `relay_key` |
| REGISTER | 22 | sync_name, sync_key, peer_id, role, direction |
| PEER_JOIN | 24 | notify client of peer in same room |
| PEER_LEAVE | 25 | peer disconnected |
| DATA | 26 | forward sync frame to target peer |
| PING | 28 | heartbeat every 15s; 45s idle read deadline (reconnect) |

Room key: `sync_name` + `sync_key`. Relay bridges sync traffic via virtual `net.Conn` per remote peer on the client. Peer IDs are persisted in SQLite (`local_identity`) so a restart is the same device. Re-registering the same ID replaces the old session without kicking the new one.

### Sync behavior

1. Watch `local_path`, debounce per top-level folder (800ms; covers Cursor atomic `.tmp` + rename)
2. Build folder batch; if `approval = ask_folder`, queue until approved
3. On **provider** peer connect: push full tree (initial sync)
4. Exchange manifests; transfer only changed files (BLAKE3 hash)
5. Apply via temp file + rename; conflicts → `path.conflict.<timestamp>`
6. Persist file state in SQLite under `data_dir`
7. Track active transfers + activity log in SQLite for `sps status -v` / `sps watch`

### Direction rules

| direction | role | Behavior |
|-----------|------|----------|
| bidirectional | any | Both sides send and receive |
| push | provider | Sends changes only |
| push | consumer | Receives only |
| pull | consumer | Initiates fetch from providers |
| pull | provider | Responds to fetch requests |

### CLI commands

| Command | Purpose |
|---------|---------|
| `sps run` | Sync daemon (foreground or OS service) |
| `sps install` / `uninstall` | Windows service / Linux systemd |
| `sps status` / `status -v` | Read live state from SQLite |
| `sps watch` | Refresh detailed status every 2s |
| `sps logs` | Tail `<data_dir>/sps.log` |
| `sps approve` | Approve folder in `ask_folder` mode |
| `sps relay run` | Start relay hub on public VPS |
| `sps relay status` | Show relay config summary |

## Changing language

Keep unchanged:
- `config.example.toml` and `relay.example.toml` schema
- Sync and relay wire protocol message types and frame formats
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
- [x] Phase 4 — initial push on connect, file logging, status/watch/logs, GitHub Releases
- [x] Phase 5 — relay server for dual-NAT (outbound hub on public VPS)
- [ ] Phase 6 — web UI, .syncignore, dry-run (optional, future)

## Changelog

| Date | Change |
|------|--------|
| 2026-06-21 | Initial Go implementation per design plan |
| 2026-06-21 | Windows service fix (svc/mgr), initial push on peer connect, gitignore `configs/` |
| 2026-06-21 | GitHub Releases workflow (Windows + Linux binaries) |
| 2026-06-24 | Monitoring: `status -v`, `watch`, `logs`, SQLite activity + transfer jobs |
| 2026-06-24 | Relay server (`sps relay run`) for sync when both peers are behind NAT |
| 2026-08-18 | v0.2.3 live-sync: relay ping/reconnect, stable peer_id, Cursor tmp+rename watcher, send/receive logs |
