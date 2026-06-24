package state

import (
	"database/sql"
	"fmt"
	"time"
)

type DaemonInfo struct {
	PeerID    string
	PID       int
	Listen    string
	StartedAt int64
	UpdatedAt int64
}

type SyncJob struct {
	ID         int64
	SyncName   string
	PeerAddr   string
	Direction  string // send | receive
	FilesTotal int
	FilesDone  int
	BytesTotal int64
	BytesDone  int64
	CurrentFile string
	Status     string // running | completed | failed
	Error      string
	StartedAt  int64
	UpdatedAt  int64
}

type ActivityEvent struct {
	ID        int64
	SyncName  string
	Level     string
	Message   string
	CreatedAt int64
}

func (s *Store) migrateActivity() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS daemon_info (
			id INTEGER PRIMARY KEY CHECK (id = 1),
			peer_id TEXT NOT NULL,
			pid INTEGER NOT NULL,
			listen_addr TEXT NOT NULL,
			started_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS sync_jobs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			sync_name TEXT NOT NULL,
			peer_addr TEXT NOT NULL,
			direction TEXT NOT NULL,
			files_total INTEGER NOT NULL,
			files_done INTEGER NOT NULL,
			bytes_total INTEGER NOT NULL,
			bytes_done INTEGER NOT NULL,
			current_file TEXT NOT NULL DEFAULT '',
			status TEXT NOT NULL,
			error TEXT NOT NULL DEFAULT '',
			started_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_sync_jobs_active ON sync_jobs(sync_name, status);
		CREATE TABLE IF NOT EXISTS activity_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			sync_name TEXT NOT NULL DEFAULT '',
			level TEXT NOT NULL,
			message TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_activity_log_time ON activity_log(created_at DESC);
	`)
	return err
}

func (s *Store) SetDaemonInfo(peerID string, pid int, listen string) error {
	now := time.Now().Unix()
	_, err := s.db.Exec(`
		INSERT INTO daemon_info (id, peer_id, pid, listen_addr, started_at, updated_at)
		VALUES (1, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
		  peer_id=excluded.peer_id, pid=excluded.pid, listen_addr=excluded.listen_addr,
		  started_at=excluded.started_at, updated_at=excluded.updated_at`,
		peerID, pid, listen, now, now,
	)
	return err
}

func (s *Store) TouchDaemon() error {
	_, err := s.db.Exec(`UPDATE daemon_info SET updated_at=? WHERE id=1`, time.Now().Unix())
	return err
}

func (s *Store) ClearDaemonInfo() error {
	_, err := s.db.Exec(`DELETE FROM daemon_info WHERE id=1`)
	return err
}

func (s *Store) GetDaemonInfo() (DaemonInfo, bool, error) {
	var d DaemonInfo
	err := s.db.QueryRow(
		`SELECT peer_id, pid, listen_addr, started_at, updated_at FROM daemon_info WHERE id=1`,
	).Scan(&d.PeerID, &d.PID, &d.Listen, &d.StartedAt, &d.UpdatedAt)
	if err == sql.ErrNoRows {
		return d, false, nil
	}
	if err != nil {
		return d, false, err
	}
	return d, true, nil
}

func (s *Store) StartSyncJob(syncName, peerAddr, direction string, filesTotal int, bytesTotal int64) (int64, error) {
	now := time.Now().Unix()
	res, err := s.db.Exec(`
		INSERT INTO sync_jobs (sync_name, peer_addr, direction, files_total, files_done, bytes_total, bytes_done,
			current_file, status, started_at, updated_at)
		VALUES (?, ?, ?, ?, 0, ?, 0, '', 'running', ?, ?)`,
		syncName, peerAddr, direction, filesTotal, bytesTotal, now, now,
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) UpdateSyncJob(jobID int64, filesDone int, bytesDone int64, currentFile string) error {
	_, err := s.db.Exec(`
		UPDATE sync_jobs SET files_done=?, bytes_done=?, current_file=?, updated_at=?
		WHERE id=? AND status='running'`,
		filesDone, bytesDone, currentFile, time.Now().Unix(), jobID,
	)
	return err
}

func (s *Store) FinishSyncJob(jobID int64, status, errMsg string) error {
	_, err := s.db.Exec(`
		UPDATE sync_jobs SET status=?, error=?, current_file='', updated_at=?
		WHERE id=?`,
		status, errMsg, time.Now().Unix(), jobID,
	)
	return err
}

func (s *Store) ListActiveJobs(syncName string) ([]SyncJob, error) {
	q := `SELECT id, sync_name, peer_addr, direction, files_total, files_done, bytes_total, bytes_done,
		current_file, status, error, started_at, updated_at FROM sync_jobs WHERE status='running'`
	args := []any{}
	if syncName != "" {
		q += ` AND sync_name=?`
		args = append(args, syncName)
	}
	q += ` ORDER BY started_at DESC`
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

func (s *Store) ListRecentJobs(syncName string, limit int) ([]SyncJob, error) {
	if limit <= 0 {
		limit = 10
	}
	q := `SELECT id, sync_name, peer_addr, direction, files_total, files_done, bytes_total, bytes_done,
		current_file, status, error, started_at, updated_at FROM sync_jobs`
	args := []any{}
	if syncName != "" {
		q += ` WHERE sync_name=?`
		args = append(args, syncName)
	}
	q += ` ORDER BY started_at DESC LIMIT ?`
	args = append(args, limit)
	rows, err := s.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanJobs(rows)
}

func scanJobs(rows *sql.Rows) ([]SyncJob, error) {
	var out []SyncJob
	for rows.Next() {
		var j SyncJob
		if err := rows.Scan(&j.ID, &j.SyncName, &j.PeerAddr, &j.Direction, &j.FilesTotal, &j.FilesDone,
			&j.BytesTotal, &j.BytesDone, &j.CurrentFile, &j.Status, &j.Error, &j.StartedAt, &j.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, j)
	}
	return out, rows.Err()
}

func (s *Store) AppendActivity(syncName, level, message string) error {
	_, err := s.db.Exec(
		`INSERT INTO activity_log (sync_name, level, message, created_at) VALUES (?, ?, ?, ?)`,
		syncName, level, message, time.Now().Unix(),
	)
	if err != nil {
		return err
	}
	// Keep activity table bounded.
	_, _ = s.db.Exec(`DELETE FROM activity_log WHERE id NOT IN (
		SELECT id FROM activity_log ORDER BY id DESC LIMIT 5000
	)`)
	return nil
}

func (s *Store) ListActivity(limit int) ([]ActivityEvent, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.db.Query(
		`SELECT id, sync_name, level, message, created_at FROM activity_log ORDER BY id DESC LIMIT ?`, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []ActivityEvent
	for rows.Next() {
		var e ActivityEvent
		if err := rows.Scan(&e.ID, &e.SyncName, &e.Level, &e.Message, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func FormatBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

func FormatProgress(done, total int64) string {
	if total <= 0 {
		return "—"
	}
	pct := float64(done) * 100 / float64(total)
	return fmt.Sprintf("%s / %s (%.0f%%)", FormatBytes(done), FormatBytes(total), pct)
}
