# SuperProjectSyncer

P2P folder sync daemon for Windows and Linux. Run the app on each machine; peers connect via **mDNS discovery** and/or **direct IP**, and sync only changed files.

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
sps run [--config PATH]       # Run daemon (foreground)
sps install [--config PATH]   # Install OS service (admin / sudo)
sps uninstall                 # Remove service
sps status [--config PATH]    # Show groups, peers, pending approvals
sps approve SYNC FOLDER       # Approve folder in ask_folder mode
```

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
