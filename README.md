# SuperProjectSyncer

P2P folder sync daemon for Windows and Linux. Peers connect via **direct IP**, **LAN discovery (mDNS)**, or a **relay hub** on a public VPS when both sides are behind NAT. Only changed files are transferred.

See [BUILD_PLAN.md](BUILD_PLAN.md) for language, architecture, and how to change the stack.

## Download (releases)

Pre-built binaries are on the [Releases](https://github.com/cybersec922/SuperProjectSyncer/releases) page:

| File | Platform |
|------|----------|
| `sps-windows-amd64.exe` | Windows 64-bit |
| `sps-linux-amd64` | Linux x86_64 |
| `sps-linux-arm64` | Linux ARM64 (e.g. Raspberry Pi, some VPS) |

**Windows:** download the `.exe`, place it anywhere (e.g. `C:\Tools\sps.exe`), then:

```powershell
.\sps.exe run --config config.toml
.\sps.exe install --config config.toml   # run PowerShell as Administrator
```

**Linux:** download, make executable, install:

```bash
chmod +x sps-linux-amd64
sudo mv sps-linux-amd64 /usr/local/bin/sps
sps run --config config.toml
sudo sps install --config /etc/superprojectsyncer/config.toml
```

Copy `config.example.toml` to `config.toml` and edit paths, peers, and `sync_key` before running.

## Quick start

```bash
# Build
go build -o bin/sps ./cmd/sps

# Copy and edit config
cp config.example.toml config.toml

# Run (foreground)
./bin/sps run --config config.toml
```

## Config

Each `[[sync]]` section is a named sync group. Machines with the same `name` and `sync_key` sync with each other.

```toml
[global]
listen = "0.0.0.0:7741"
data_dir = "~/.superprojectsyncer"
discovery = true
# log_file = ""              # default: <data_dir>/sps.log
# relay = "vps-ip:7750"      # optional NAT hub (see Relay section)
# relay_key = "relay-secret"

[[sync]]
name = "sync-example"
local_path = "D:\\tools\\rainmeter"   # Windows
# local_path = "/home/user/rainmeter" # Linux on the other machine
direction = "bidirectional"             # push | pull | bidirectional
role = "provider"                       # provider | consumer (for one-way)
approval = "auto"                       # auto | ask_folder
peers = ["192.168.1.50:7741"]
sync_key = "change-me-long-random"
ignore = [".env", "*.log", "node_modules/**", "secrets/"]
```

### Direction and role

| Setup | Config |
|-------|--------|
| Two-way sync | `direction = "bidirectional"` |
| Dev pushes to prod | Dev: `direction = "push"`, `role = "provider"` — Prod: `direction = "push"`, `role = "consumer"` |
| Prod pull from dev | Prod: `direction = "pull"`, `role = "consumer"` |

Keep machine-specific files (e.g. `.env`) out of sync with `ignore`.

### Approval modes

| Mode | Behavior |
|------|----------|
| `auto` | Sync folder changes immediately |
| `ask_folder` | Queue changes per **folder** until approved |

```bash
sps approve sync-example myfolder
```

## Commands

```bash
sps run [--config PATH]           # Run sync daemon (foreground or service)
sps install [--config PATH]       # Install OS service (admin / sudo)
sps uninstall                     # Remove service
sps status [--config PATH]        # Brief sync status (works while service runs)
sps status -v [--config PATH]     # Detailed: peers, transfers, file progress
sps watch [--config PATH]         # Live refresh every 2s (Ctrl+C to stop)
sps logs [--config PATH] [-n 50]  # Show last N lines of sps.log
sps approve SYNC FOLDER           # Approve folder in ask_folder mode
sps relay run [--config PATH]     # Run relay hub on a public VPS
sps relay status [--config PATH]  # Show relay config
sps version                       # Print version
```

**Monitoring** (Windows & Linux — same commands):

- Log file defaults to `<data_dir>/sps.log` (e.g. `~/.superprojectsyncer/sps.log` or `/root/.superprojectsyncer/sps.log`)
- `sps status -v` shows active sync jobs: files done/left, bytes transferred, current filename
- `sps watch` is useful during a large initial sync
- Set `log_file = "none"` in config to disable file logging

## Relay server (both peers behind NAT)

When **neither** machine has a public IP, run a **relay** on your VPS. Both peers connect **outbound** to the relay — no port forwarding on home networks.

**1. On VPS** (public IP, open port `7750/tcp`):

```bash
cp relay.example.toml relay.toml
# edit key = "your-long-secret"
sps relay run --config relay.toml
```

Or as a systemd service:

```bash
sps relay run --listen 0.0.0.0:7750 --key your-long-secret
```

**2. On each peer** (Windows + Linux configs):

```toml
[global]
relay = "75.119.139.223:7750"
relay_key = "your-long-secret"   # must match relay.toml key

[[sync]]
name = "cronwebapp"
# peers = []   # not needed — relay matches same name + sync_key
sync_key = "CRAZYMFTAPMAN_HEHEHEHAHHAHAHA69659"
```

Peers in the same `name` + `sync_key` room are connected through the relay automatically. You can still use direct `peers = [...]` alongside relay for hybrid setups.

| Mode | When to use |
|------|-------------|
| Direct `peers` | One side has public IP (your current Windows→Linux push) |
| `relay` | Both behind NAT / no inbound ports |
| Both | Relay as fallback + direct when reachable |

### Networking quick reference

| Scenario | What to open | Client config |
|----------|--------------|---------------|
| Push to VPS (one public IP) | Inbound `7741` on server | Provider: `peers = ["server:7741"]` |
| Both behind NAT | Inbound `7750` on VPS (relay) | Both: `relay` + `relay_key`; `peers = []` OK |
| Same LAN | Nothing (mDNS) | `discovery = true`, same `name` + `sync_key` |

## Service install

**Windows** (elevated PowerShell):

```powershell
sps install --config C:\path\to\config.toml
```

**Linux** (sudo):

```bash
sudo sps install --config /etc/superprojectsyncer/config.toml
```

## Example: Windows + Linux

**Windows** `config.toml`:

```toml
[[sync]]
name = "sync-example"
local_path = "D:\\tools\\rainmeter"
direction = "bidirectional"
approval = "auto"
peers = ["192.168.1.50:7741"]
sync_key = "your-shared-secret"
ignore = [".env", "node_modules/**"]
```

**Linux** `config.toml`:

```toml
[[sync]]
name = "sync-example"
local_path = "/home/usernamehehe/rainmeter"
direction = "bidirectional"
approval = "auto"
peers = ["192.168.1.10:7741"]
sync_key = "your-shared-secret"
ignore = [".env", "node_modules/**"]
```

Run `sps run` on both machines. They discover each other on LAN (same `name`) and also dial configured `peers`.

## Multiple sync groups

Add multiple `[[sync]]` blocks — each runs independently with its own folder, peers, and direction:

```toml
[[sync]]
name = "project-a"
local_path = "/data/project-a"
# ...

[[sync]]
name = "project-b"
local_path = "/data/project-b"
# ...
```

## Build

```bash
go build -o bin/sps ./cmd/sps

# Cross-compile
GOOS=linux GOARCH=amd64 go build -o bin/sps-linux ./cmd/sps
GOOS=windows GOARCH=amd64 go build -o bin/sps.exe ./cmd/sps
```

Details: [BUILD_PLAN.md](BUILD_PLAN.md)

## Config templates

| File | Use |
|------|-----|
| `config.example.toml` | Sync daemon on each machine |
| `relay.example.toml` | Relay hub on a public VPS |
