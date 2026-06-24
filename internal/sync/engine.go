package syncengine

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/superdata/superprojectsyncer/internal/approval"
	"github.com/superdata/superprojectsyncer/internal/config"
	"github.com/superdata/superprojectsyncer/internal/ignore"
	"github.com/superdata/superprojectsyncer/internal/protocol"
	"github.com/superdata/superprojectsyncer/internal/state"
	"lukechampine.com/blake3"
)

const chunkSize = 256 * 1024

type Peer struct {
	ID   string
	Addr string
	Conn net.Conn
}

type Engine struct {
	Cfg      config.Sync
	PeerID   string
	Store    *state.Store
	Ignore   *ignore.Matcher
	Approval *approval.Queue

	mu    sync.Mutex
	peers map[string]*Peer
}

func New(cfg config.Sync, peerID string, store *state.Store, appr *approval.Queue) *Engine {
	return &Engine{
		Cfg:      cfg,
		PeerID:   peerID,
		Store:    store,
		Ignore:   ignore.New(cfg.Ignore),
		Approval: appr,
		peers:    make(map[string]*Peer),
	}
}

func (e *Engine) Root() string       { return e.Cfg.LocalPath }
func (e *Engine) SyncName() string   { return e.Cfg.Name }

func (e *Engine) AddPeer(p *Peer) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if old, ok := e.peers[p.ID]; ok && old.Conn != nil {
		old.Conn.Close()
	}
	e.peers[p.ID] = p
	if e.Store != nil {
		_ = e.Store.UpsertPeer(e.Cfg.Name, p.ID, p.Addr)
	}
}

func (e *Engine) RemovePeer(peerID string) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if p, ok := e.peers[peerID]; ok {
		if p.Conn != nil {
			p.Conn.Close()
		}
		delete(e.peers, peerID)
	}
}

func (e *Engine) PeerCount() int {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(e.peers)
}

func (e *Engine) ListPeers() []*Peer {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]*Peer, 0, len(e.peers))
	for _, p := range e.peers {
		out = append(out, p)
	}
	return out
}

func (e *Engine) SendHello(p *Peer) error {
	return protocol.WriteJSON(p.Conn, protocol.TypeHello, protocol.Hello{
		SyncName:  e.Cfg.Name,
		PeerID:    e.PeerID,
		Direction: string(e.Cfg.Direction),
		Role:      string(e.Cfg.Role),
		Version:   protocol.Version,
	})
}

func (e *Engine) OnFolderChanged(folder string) {
	if !e.Cfg.CanSend() {
		return
	}
	if e.Approval != nil {
		e.Approval.Wait(folder)
	}
	e.broadcastFolder(folder)
}

// OnPeerConnected pushes the full tree when a provider gains a new peer (initial sync).
func (e *Engine) OnPeerConnected() {
	if !e.Cfg.CanSend() {
		return
	}
	if e.Approval != nil {
		e.Approval.Wait(".")
	}
	manifest, err := e.buildManifestForFolder(".")
	if err != nil {
		log.Printf("[%s] initial push manifest: %v", e.Cfg.Name, err)
		return
	}
	log.Printf("[%s] initial push to peer: %d files", e.Cfg.Name, len(manifest.Files))
	e.logActivity("info", "initial push: %d files", len(manifest.Files))
	e.broadcastFolder(".")
}

func (e *Engine) broadcastFolder(folder string) {
	manifest, err := e.buildManifestForFolder(folder)
	if err != nil {
		log.Printf("[%s] build manifest %s: %v", e.Cfg.Name, folder, err)
		return
	}
	if len(manifest.Files) == 0 {
		return
	}
	e.mu.Lock()
	peers := make([]*Peer, 0, len(e.peers))
	for _, p := range e.peers {
		peers = append(peers, p)
	}
	e.mu.Unlock()
	for _, p := range peers {
		go e.sendManifest(p, manifest)
	}
}

func (e *Engine) buildManifestForFolder(folder string) (protocol.Manifest, error) {
	var files []protocol.FileEntry
	root := e.Cfg.LocalPath
	walkRoot := root
	if folder != "." {
		walkRoot = filepath.Join(root, folder)
	}
	if _, err := os.Stat(walkRoot); os.IsNotExist(err) {
		return protocol.Manifest{SyncName: e.Cfg.Name}, nil
	}
	err := filepath.Walk(walkRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if e.Ignore.Ignored(rel) {
			return nil
		}
		hash, size, mtime, err := fileMeta(path, info)
		if err != nil {
			return err
		}
		files = append(files, protocol.FileEntry{Path: rel, Hash: hash, Size: size, Mtime: mtime})
		if e.Store != nil {
			_ = e.Store.UpsertFile(e.Cfg.Name, state.FileRecord{
				RelPath: rel, Hash: hash, Size: size, Mtime: mtime,
			})
		}
		return nil
	})
	return protocol.Manifest{SyncName: e.Cfg.Name, Files: files}, err
}

func (e *Engine) BuildFullManifest() (protocol.Manifest, error) {
	return e.buildManifestForFolder(".")
}

func fileMeta(path string, info os.FileInfo) (string, int64, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, 0, err
	}
	defer f.Close()
	h := blake3.New(32, nil)
	if _, err := io.Copy(h, f); err != nil {
		return "", 0, 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), info.Size(), info.ModTime().Unix(), nil
}

func (e *Engine) logActivity(level, format string, args ...any) {
	if e.Store == nil {
		return
	}
	msg := fmt.Sprintf(format, args...)
	_ = e.Store.AppendActivity(e.Cfg.Name, level, msg)
}

func (e *Engine) sendManifest(p *Peer, manifest protocol.Manifest) {
	if err := protocol.WriteJSON(p.Conn, protocol.TypeManifest, manifest); err != nil {
		log.Printf("[%s] send manifest to %s: %v", e.Cfg.Name, p.Addr, err)
		e.RemovePeer(p.ID)
	}
}

func (e *Engine) HandleIncoming(p *Peer) {
	defer e.RemovePeer(p.ID)
	for {
		msgType, payload, err := protocol.ReadFrame(p.Conn)
		if err != nil {
			return
		}
		switch msgType {
		case protocol.TypeHello:
			var h protocol.Hello
			if err := json.Unmarshal(payload, &h); err != nil {
				return
			}
			if h.SyncName != e.Cfg.Name {
				return
			}
			e.AddPeer(p)
		case protocol.TypeManifest:
			if !e.Cfg.CanReceive() {
				continue
			}
			var m protocol.Manifest
			if err := json.Unmarshal(payload, &m); err != nil {
				return
			}
			if m.SyncName != e.Cfg.Name {
				continue
			}
			e.applyManifest(p, m)
		case protocol.TypeNeed:
			if !e.Cfg.CanSend() {
				continue
			}
			var n protocol.Need
			if err := json.Unmarshal(payload, &n); err != nil {
				return
			}
			e.sendFiles(p, n.Paths)
		case protocol.TypePing:
			_ = protocol.WritePing(p.Conn)
		}
	}
}

func (e *Engine) applyManifest(p *Peer, remote protocol.Manifest) {
	var needed []string
	var bytesTotal int64
	for _, f := range remote.Files {
		if e.Ignore.Ignored(f.Path) {
			continue
		}
		localPath := filepath.Join(e.Cfg.LocalPath, filepath.FromSlash(f.Path))
		if shouldSkip(localPath, f, e.Cfg.Direction) {
			continue
		}
		existing, exists, _ := e.localFileEntry(localPath, f.Path)
		if !exists || existing.Hash != f.Hash {
			if exists && e.Cfg.Direction == config.DirectionBidirectional {
				if existing.Mtime > f.Mtime {
					continue
				}
				if existing.Mtime == f.Mtime && existing.Hash != f.Hash {
					e.writeConflict(localPath, f.Path)
					continue
				}
			}
			needed = append(needed, f.Path)
			bytesTotal += f.Size
		}
	}
	if len(needed) == 0 {
		return
	}
	e.logActivity("info", "receiving %d files (%s) from %s", len(needed), state.FormatBytes(bytesTotal), p.Addr)
	var jobID int64
	if e.Store != nil {
		jobID, _ = e.Store.StartSyncJob(e.Cfg.Name, p.Addr, "receive", len(needed), bytesTotal)
	}
	if err := protocol.WriteJSON(p.Conn, protocol.TypeNeed, protocol.Need{Paths: needed}); err != nil {
		log.Printf("[%s] request files: %v", e.Cfg.Name, err)
		if jobID != 0 {
			_ = e.Store.FinishSyncJob(jobID, "failed", err.Error())
		}
		return
	}
	var doneBytes int64
	for i, path := range needed {
		fileSize := int64(0)
		for _, f := range remote.Files {
			if f.Path == path {
				fileSize = f.Size
				break
			}
		}
		if jobID != 0 {
			_ = e.Store.UpdateSyncJob(jobID, i, doneBytes, path)
		}
		if err := e.receiveFile(p.Conn, path); err != nil {
			log.Printf("[%s] receive %s: %v", e.Cfg.Name, path, err)
			e.logActivity("error", "receive failed %s: %v", path, err)
			if jobID != 0 {
				_ = e.Store.FinishSyncJob(jobID, "failed", err.Error())
			}
			return
		}
		doneBytes += fileSize
		if jobID != 0 {
			_ = e.Store.UpdateSyncJob(jobID, i+1, doneBytes, "")
		}
		e.logActivity("info", "received %s (%s)", path, state.FormatBytes(fileSize))
	}
	if jobID != 0 {
		_ = e.Store.FinishSyncJob(jobID, "completed", "")
	}
	e.logActivity("info", "receive complete: %d files from %s", len(needed), p.Addr)
}

func shouldSkip(localPath string, remote protocol.FileEntry, dir config.Direction) bool {
	fi, err := os.Stat(localPath)
	if err != nil {
		return false
	}
	if dir != config.DirectionBidirectional {
		return false
	}
	localHash, _, mtime, err := fileMeta(localPath, fi)
	if err != nil {
		return false
	}
	return localHash == remote.Hash && mtime >= remote.Mtime
}

func (e *Engine) localFileEntry(absPath, relPath string) (protocol.FileEntry, bool, error) {
	fi, err := os.Stat(absPath)
	if os.IsNotExist(err) {
		return protocol.FileEntry{}, false, nil
	}
	if err != nil {
		return protocol.FileEntry{}, false, err
	}
	hash, size, mtime, err := fileMeta(absPath, fi)
	if err != nil {
		return protocol.FileEntry{}, false, err
	}
	return protocol.FileEntry{Path: relPath, Hash: hash, Size: size, Mtime: mtime}, true, nil
}

func (e *Engine) writeConflict(localPath, relPath string) {
	conflict := localPath + ".conflict." + fmt.Sprintf("%d", time.Now().Unix())
	_ = os.Rename(localPath, conflict)
	log.Printf("[%s] conflict: saved local as %s", e.Cfg.Name, conflict)
}

func (e *Engine) receiveFile(conn net.Conn, relPath string) error {
	for {
		msgType, payload, err := protocol.ReadFrame(conn)
		if err != nil {
			return err
		}
		if msgType != protocol.TypeChunk {
			continue
		}
		var chunk protocol.Chunk
		if err := json.Unmarshal(payload, &chunk); err != nil {
			return err
		}
		if chunk.Path != relPath {
			continue
		}
		dest := filepath.Join(e.Cfg.LocalPath, filepath.FromSlash(relPath))
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		tmp := dest + ".sps.tmp"
		f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			return err
		}
		if _, err := f.Write(chunk.Data); err != nil {
			f.Close()
			return err
		}
		f.Close()
		if chunk.Final {
			if err := os.Rename(tmp, dest); err != nil {
				return err
			}
			fi, err := os.Stat(dest)
			if err == nil && e.Store != nil {
				hash, size, mtime, _ := fileMeta(dest, fi)
				_ = e.Store.UpsertFile(e.Cfg.Name, state.FileRecord{
					RelPath: relPath, Hash: hash, Size: size, Mtime: mtime,
				})
			}
			return protocol.WriteJSON(conn, protocol.TypeApplyOK, map[string]string{"path": relPath})
		}
	}
}

func (e *Engine) sendFiles(p *Peer, paths []string) {
	var bytesTotal int64
	sizes := make(map[string]int64, len(paths))
	for _, rel := range paths {
		if e.Ignore.Ignored(rel) {
			continue
		}
		abs := filepath.Join(e.Cfg.LocalPath, filepath.FromSlash(rel))
		fi, err := os.Stat(abs)
		if err == nil {
			sizes[rel] = fi.Size()
			bytesTotal += fi.Size()
		}
	}
	sendCount := len(sizes)
	if sendCount == 0 {
		return
	}
	e.logActivity("info", "sending %d files (%s) to %s", sendCount, state.FormatBytes(bytesTotal), p.Addr)
	var jobID int64
	if e.Store != nil {
		jobID, _ = e.Store.StartSyncJob(e.Cfg.Name, p.Addr, "send", sendCount, bytesTotal)
	}
	var doneBytes int64
	doneFiles := 0
	for _, rel := range paths {
		if e.Ignore.Ignored(rel) {
			continue
		}
		abs := filepath.Join(e.Cfg.LocalPath, filepath.FromSlash(rel))
		data, err := os.ReadFile(abs)
		if err != nil {
			log.Printf("[%s] read %s: %v", e.Cfg.Name, rel, err)
			continue
		}
		if jobID != 0 {
			_ = e.Store.UpdateSyncJob(jobID, doneFiles, doneBytes, rel)
		}
		chunk := protocol.Chunk{Path: rel, Offset: 0, Data: data, Final: true}
		if err := protocol.WriteJSON(p.Conn, protocol.TypeChunk, chunk); err != nil {
			log.Printf("[%s] send chunk %s: %v", e.Cfg.Name, rel, err)
			if jobID != 0 {
				_ = e.Store.FinishSyncJob(jobID, "failed", err.Error())
			}
			return
		}
		doneFiles++
		doneBytes += int64(len(data))
		if jobID != 0 {
			_ = e.Store.UpdateSyncJob(jobID, doneFiles, doneBytes, "")
		}
		e.logActivity("info", "sent %s (%s)", rel, state.FormatBytes(int64(len(data))))
	}
	if jobID != 0 {
		_ = e.Store.FinishSyncJob(jobID, "completed", "")
	}
	e.logActivity("info", "send complete: %d files to %s", doneFiles, p.Addr)
}

// PullFromPeers requests full manifest from connected providers.
func (e *Engine) PullFromPeers() {
	if !e.Cfg.CanInitiatePull() {
		return
	}
	local, err := e.BuildFullManifest()
	if err != nil {
		return
	}
	localIndex := map[string]protocol.FileEntry{}
	for _, f := range local.Files {
		localIndex[f.Path] = f
	}
	e.mu.Lock()
	peers := make([]*Peer, 0, len(e.peers))
	for _, p := range e.peers {
		peers = append(peers, p)
	}
	e.mu.Unlock()
	for _, p := range peers {
		// Request remote to send manifest by sending our hello ping cycle
		_ = protocol.WritePing(p.Conn)
	}
}

func (e *Engine) EnsureRoot() error {
	return os.MkdirAll(e.Cfg.LocalPath, 0o755)
}

func (e *Engine) InitialScan() error {
	manifest, err := e.BuildFullManifest()
	if err != nil {
		return err
	}
	log.Printf("[%s] initial scan: %d files tracked", e.Cfg.Name, len(manifest.Files))
	return nil
}

func (e *Engine) ApproveFolder(folder string) error {
	if e.Approval == nil {
		return fmt.Errorf("no approval queue")
	}
	if err := e.Approval.Approve(folder); err != nil {
		return err
	}
	e.broadcastFolder(folder)
	return nil
}

func (e *Engine) PendingFolders() ([]string, error) {
	if e.Approval == nil {
		return nil, nil
	}
	return e.Approval.Pending()
}

func (e *Engine) ConnectAndHello(addr string, dial func(string, string, time.Duration) (net.Conn, error)) (*Peer, error) {
	conn, err := dial(addr, e.Cfg.SyncKey, 10*time.Second)
	if err != nil {
		return nil, err
	}
	p := &Peer{ID: addr, Addr: addr, Conn: conn}
	if err := e.SendHello(p); err != nil {
		conn.Close()
		return nil, err
	}
	e.AddPeer(p)
	go e.OnPeerConnected()
	go e.HandleIncoming(p)
	return p, nil
}

func (e *Engine) SyncKey() string {
	return e.Cfg.SyncKey
}

func (e *Engine) CanSend() bool    { return e.Cfg.CanSend() }
func (e *Engine) CanReceive() bool { return e.Cfg.CanReceive() }

func RelPathClean(p string) string {
	return filepath.ToSlash(strings.TrimPrefix(p, "./"))
}
