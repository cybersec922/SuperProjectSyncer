package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/superdata/superprojectsyncer/internal/config"
	"github.com/superdata/superprojectsyncer/internal/logging"
	"github.com/superdata/superprojectsyncer/internal/state"
)

// StatusReport is built from SQLite so it works while the daemon service is running.
func StatusReport(cfg *config.Config, store *state.Store, verbose bool) string {
	var b strings.Builder

	daemon, hasDaemon, _ := store.GetDaemonInfo()
	if hasDaemon {
		age := time.Since(time.Unix(daemon.UpdatedAt, 0))
		stateLabel := "running"
		if age > 2*time.Minute {
			stateLabel = "stale (daemon may be stopped)"
		}
		b.WriteString(fmt.Sprintf("daemon: %s  peer_id=%s  pid=%d  listen=%s  started=%s\n",
			stateLabel, daemon.PeerID, daemon.PID, daemon.Listen,
			time.Unix(daemon.StartedAt, 0).Format(time.RFC3339)))
	} else {
		b.WriteString("daemon: not running (no active session in state DB)\n")
	}

	logPath := logging.ResolvePath(cfg.Global.DataDir, cfg.Global.LogFile)
	if logPath != "" {
		b.WriteString(fmt.Sprintf("log_file: %s\n", logPath))
	}
	if cfg.Global.Relay != "" {
		b.WriteString(fmt.Sprintf("relay: %s\n", cfg.Global.Relay))
	}

	summary, _ := store.StatusSummary()
	b.WriteString(summary + "\n")

	for _, syncCfg := range cfg.Syncs {
		b.WriteString("\n")
		b.WriteString(formatSyncGroup(store, syncCfg, verbose))
	}

	if verbose {
		b.WriteString("\n--- recent activity ---\n")
		events, _ := store.ListActivity(30)
		if len(events) == 0 {
			b.WriteString("(no activity yet)\n")
		} else {
			for i := len(events) - 1; i >= 0; i-- {
				e := events[i]
				ts := time.Unix(e.CreatedAt, 0).Format("15:04:05")
				prefix := e.SyncName
				if prefix != "" {
					prefix = "[" + prefix + "] "
				}
				b.WriteString(fmt.Sprintf("%s %s%s\n", ts, prefix, e.Message))
			}
		}
	}

	return strings.TrimRight(b.String(), "\n")
}

func formatSyncGroup(store *state.Store, syncCfg config.Sync, verbose bool) string {
	var b strings.Builder
	files, _ := store.ListFiles(syncCfg.Name)
	peers, _ := store.ListPeers(syncCfg.Name)
	pending, _ := store.ListPending(syncCfg.Name)

	b.WriteString(fmt.Sprintf("[%s]\n", syncCfg.Name))
	b.WriteString(fmt.Sprintf("  path:      %s\n", syncCfg.LocalPath))
	b.WriteString(fmt.Sprintf("  direction: %s  role: %s  approval: %s\n",
		syncCfg.Direction, syncCfg.Role, syncCfg.Approval))
	b.WriteString(fmt.Sprintf("  tracked:   %d files\n", len(files)))
	b.WriteString(fmt.Sprintf("  pending:   %v\n", pending))

	if len(peers) == 0 {
		b.WriteString("  peers:     (none connected recently)\n")
	} else {
		b.WriteString("  peers:\n")
		for _, p := range peers {
			ago := time.Since(time.Unix(p.LastSeen, 0)).Round(time.Second)
			b.WriteString(fmt.Sprintf("    - %s  id=%s  last_seen=%s ago\n", p.Addr, p.PeerID, ago))
		}
	}

	active, _ := store.ListActiveJobs(syncCfg.Name)
	if len(active) > 0 {
		b.WriteString("  active sync:\n")
		for _, j := range active {
			b.WriteString(formatJob(j))
		}
	} else if verbose {
		recent, _ := store.ListRecentJobs(syncCfg.Name, 3)
		if len(recent) > 0 {
			b.WriteString("  recent sync:\n")
			for _, j := range recent {
				b.WriteString(formatJob(j))
			}
		}
	}

	return b.String()
}

func formatJob(j state.SyncJob) string {
	var b strings.Builder
	elapsed := time.Since(time.Unix(j.StartedAt, 0)).Round(time.Second)
	b.WriteString(fmt.Sprintf("    %s %s → %s  status=%s  elapsed=%s\n",
		j.Direction, j.SyncName, j.PeerAddr, j.Status, elapsed))
	if j.FilesTotal > 0 {
		remaining := j.FilesTotal - j.FilesDone
		b.WriteString(fmt.Sprintf("      files: %d/%d done (%d left)\n",
			j.FilesDone, j.FilesTotal, remaining))
	}
	if j.BytesTotal > 0 {
		b.WriteString(fmt.Sprintf("      data:  %s\n", state.FormatProgress(j.BytesDone, j.BytesTotal)))
	}
	if j.CurrentFile != "" {
		b.WriteString(fmt.Sprintf("      now:   %s\n", j.CurrentFile))
	}
	if j.Error != "" {
		b.WriteString(fmt.Sprintf("      error: %s\n", j.Error))
	}
	return b.String()
}
