package state

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dataDir, "state.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) migrate() error {
	_, err := s.db.Exec(`
		CREATE TABLE IF NOT EXISTS file_state (
			sync_name TEXT NOT NULL,
			rel_path TEXT NOT NULL,
			hash TEXT NOT NULL,
			size INTEGER NOT NULL,
			mtime INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (sync_name, rel_path)
		);
		CREATE TABLE IF NOT EXISTS pending_approval (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			sync_name TEXT NOT NULL,
			folder TEXT NOT NULL,
			created_at INTEGER NOT NULL
		);
		CREATE TABLE IF NOT EXISTS peer_status (
			sync_name TEXT NOT NULL,
			peer_id TEXT NOT NULL,
			addr TEXT NOT NULL,
			last_seen INTEGER NOT NULL,
			PRIMARY KEY (sync_name, peer_id)
		);
	`)
	return err
}

type FileRecord struct {
	RelPath string
	Hash    string
	Size    int64
	Mtime   int64
}

func (s *Store) UpsertFile(syncName string, rec FileRecord) error {
	_, err := s.db.Exec(
		`INSERT INTO file_state (sync_name, rel_path, hash, size, mtime, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?)
		 ON CONFLICT(sync_name, rel_path) DO UPDATE SET
		   hash=excluded.hash, size=excluded.size, mtime=excluded.mtime, updated_at=excluded.updated_at`,
		syncName, rec.RelPath, rec.Hash, rec.Size, rec.Mtime, time.Now().Unix(),
	)
	return err
}

func (s *Store) GetFile(syncName, relPath string) (FileRecord, bool, error) {
	var rec FileRecord
	err := s.db.QueryRow(
		`SELECT rel_path, hash, size, mtime FROM file_state WHERE sync_name=? AND rel_path=?`,
		syncName, relPath,
	).Scan(&rec.RelPath, &rec.Hash, &rec.Size, &rec.Mtime)
	if err == sql.ErrNoRows {
		return rec, false, nil
	}
	if err != nil {
		return rec, false, err
	}
	return rec, true, nil
}

func (s *Store) ListFiles(syncName string) ([]FileRecord, error) {
	rows, err := s.db.Query(
		`SELECT rel_path, hash, size, mtime FROM file_state WHERE sync_name=?`, syncName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []FileRecord
	for rows.Next() {
		var rec FileRecord
		if err := rows.Scan(&rec.RelPath, &rec.Hash, &rec.Size, &rec.Mtime); err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, rows.Err()
}

func (s *Store) DeleteFile(syncName, relPath string) error {
	_, err := s.db.Exec(`DELETE FROM file_state WHERE sync_name=? AND rel_path=?`, syncName, relPath)
	return err
}

func (s *Store) AddPending(syncName, folder string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO pending_approval (sync_name, folder, created_at) VALUES (?, ?, ?)`,
		syncName, folder, time.Now().Unix(),
	)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

func (s *Store) ListPending(syncName string) ([]string, error) {
	rows, err := s.db.Query(
		`SELECT folder FROM pending_approval WHERE sync_name=? ORDER BY id`, syncName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var folders []string
	for rows.Next() {
		var f string
		if err := rows.Scan(&f); err != nil {
			return nil, err
		}
		folders = append(folders, f)
	}
	return folders, rows.Err()
}

func (s *Store) RemovePending(syncName, folder string) error {
	_, err := s.db.Exec(`DELETE FROM pending_approval WHERE sync_name=? AND folder=?`, syncName, folder)
	return err
}

func (s *Store) UpsertPeer(syncName, peerID, addr string) error {
	_, err := s.db.Exec(
		`INSERT INTO peer_status (sync_name, peer_id, addr, last_seen) VALUES (?, ?, ?, ?)
		 ON CONFLICT(sync_name, peer_id) DO UPDATE SET addr=excluded.addr, last_seen=excluded.last_seen`,
		syncName, peerID, addr, time.Now().Unix(),
	)
	return err
}

type PeerInfo struct {
	PeerID   string
	Addr     string
	LastSeen int64
}

func (s *Store) ListPeers(syncName string) ([]PeerInfo, error) {
	rows, err := s.db.Query(`SELECT peer_id, addr, last_seen FROM peer_status WHERE sync_name=?`, syncName)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []PeerInfo
	for rows.Next() {
		var p PeerInfo
		if err := rows.Scan(&p.PeerID, &p.Addr, &p.LastSeen); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) StatusSummary() (string, error) {
	var syncCount, fileCount, pendingCount int
	if err := s.db.QueryRow(`SELECT COUNT(DISTINCT sync_name) FROM file_state`).Scan(&syncCount); err != nil {
		return "", err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM file_state`).Scan(&fileCount); err != nil {
		return "", err
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM pending_approval`).Scan(&pendingCount); err != nil {
		return "", err
	}
	return fmt.Sprintf("sync_groups=%d tracked_files=%d pending_approvals=%d", syncCount, fileCount, pendingCount), nil
}
