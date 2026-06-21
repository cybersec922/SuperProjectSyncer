package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/superdata/superprojectsyncer/internal/approval"
	"github.com/superdata/superprojectsyncer/internal/config"
	"github.com/superdata/superprojectsyncer/internal/discovery"
	"github.com/superdata/superprojectsyncer/internal/protocol"
	"github.com/superdata/superprojectsyncer/internal/state"
	syncengine "github.com/superdata/superprojectsyncer/internal/sync"
	"github.com/superdata/superprojectsyncer/internal/transport"
	"github.com/superdata/superprojectsyncer/internal/watcher"
)

type Group struct {
	Cfg       config.Sync
	Engine    *syncengine.Engine
	Approval  *approval.Queue
	Discovery *discovery.Manager
	Watcher   *watcher.Watcher
}

type App struct {
	Config  *config.Config
	Store   *state.Store
	PeerID  string
	groups  []*Group
	groupBy map[string]*Group
	ln      *transport.Listener
	mu      sync.Mutex
	dialing map[string]bool
}

func New(cfg *config.Config) (*App, error) {
	store, err := state.Open(cfg.Global.DataDir)
	if err != nil {
		return nil, fmt.Errorf("open state: %w", err)
	}
	return &App{
		Config:  cfg,
		Store:   store,
		PeerID:  uuid.NewString(),
		dialing: make(map[string]bool),
		groupBy: make(map[string]*Group),
	}, nil
}

func (a *App) Close() error {
	for _, g := range a.groups {
		g.Stop()
	}
	if a.ln != nil {
		a.ln.Close()
	}
	return a.Store.Close()
}

func (a *App) Groups() []*Group { return a.groups }

func (a *App) FindGroup(name string) *Group {
	return a.groupBy[name]
}

func (a *App) Start(ctx context.Context) error {
	port, err := discovery.ParseListenPort(a.Config.Global.Listen)
	if err != nil {
		return err
	}
	if len(a.Config.Syncs) == 0 {
		return fmt.Errorf("no sync groups")
	}

	ln, err := transport.Listen(a.Config.Global.Listen, a.Config.Syncs[0].SyncKey)
	if err != nil {
		return err
	}
	a.ln = ln
	go a.acceptLoop(ctx, ln)

	for _, syncCfg := range a.Config.Syncs {
		g, err := a.startGroup(ctx, syncCfg, port)
		if err != nil {
			return fmt.Errorf("start group %q: %w", syncCfg.Name, err)
		}
		a.groups = append(a.groups, g)
		a.groupBy[syncCfg.Name] = g
	}

	go a.peerDialLoop(ctx)
	return nil
}

func (a *App) startGroup(ctx context.Context, syncCfg config.Sync, port int) (*Group, error) {
	onPending := func(folder string) {
		log.Printf("[%s] pending approval for folder: %s (run: sps approve %s %s)", syncCfg.Name, folder, syncCfg.Name, folder)
	}
	appr := approval.New(syncCfg.Approval, a.Store, syncCfg.Name, onPending)
	engine := syncengine.New(syncCfg, a.PeerID, a.Store, appr)
	if err := engine.EnsureRoot(); err != nil {
		return nil, err
	}
	if err := engine.InitialScan(); err != nil {
		return nil, err
	}

	g := &Group{Cfg: syncCfg, Engine: engine, Approval: appr}

	w, err := watcher.New(syncCfg.LocalPath, func(folder string) {
		engine.OnFolderChanged(folder)
	})
	if err != nil {
		return nil, err
	}
	g.Watcher = w
	go w.Run()

	if syncCfg.DiscoveryEnabled(a.Config.Global.Discovery) {
		dm := discovery.New(syncCfg.Name, port, func(addr string) {
			a.tryDialGroup(g, addr)
		})
		if err := dm.Start(); err != nil {
			log.Printf("[%s] discovery: %v", syncCfg.Name, err)
		} else {
			g.Discovery = dm
		}
	}

	for _, peer := range syncCfg.Peers {
		a.tryDialGroup(g, peer)
	}
	return g, nil
}

func (a *App) acceptLoop(ctx context.Context, ln *transport.Listener) {
	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return
			default:
				log.Printf("accept: %v", err)
				time.Sleep(time.Second)
				continue
			}
		}
		go a.handleInbound(conn)
	}
}

func (a *App) handleInbound(conn net.Conn) {
	addr := conn.RemoteAddr().String()
	msgType, payload, err := protocol.ReadFrame(conn)
	if err != nil {
		conn.Close()
		return
	}
	if msgType != protocol.TypeHello {
		conn.Close()
		return
	}
	var hello protocol.Hello
	if err := json.Unmarshal(payload, &hello); err != nil {
		conn.Close()
		return
	}
	g := a.FindGroup(hello.SyncName)
	if g == nil {
		log.Printf("unknown sync group from %s: %s", addr, hello.SyncName)
		conn.Close()
		return
	}
	peerID := hello.PeerID
	if peerID == "" {
		peerID = addr
	}
	p := &syncengine.Peer{ID: peerID, Addr: addr, Conn: conn}
	g.Engine.AddPeer(p)
	log.Printf("[%s] inbound peer %s from %s", g.Cfg.Name, peerID, addr)
	go g.Engine.OnPeerConnected()
	go g.Engine.HandleIncoming(p)
}

func (a *App) peerDialLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			for _, g := range a.groups {
				for _, peer := range g.Cfg.Peers {
					a.tryDialGroup(g, peer)
				}
				if g.Cfg.CanInitiatePull() {
					g.Engine.PullFromPeers()
				}
			}
		}
	}
}

func (a *App) tryDialGroup(g *Group, addr string) {
	key := g.Cfg.Name + "|" + addr
	a.mu.Lock()
	if a.dialing[key] {
		a.mu.Unlock()
		return
	}
	for _, p := range g.Engine.ListPeers() {
		if p.Addr == addr {
			a.mu.Unlock()
			return
		}
	}
	a.dialing[key] = true
	a.mu.Unlock()

	go func() {
		defer func() {
			a.mu.Lock()
			delete(a.dialing, key)
			a.mu.Unlock()
		}()
		p, err := g.Engine.ConnectAndHello(addr, transport.Dial)
		if err != nil {
			log.Printf("[%s] dial %s: %v", g.Cfg.Name, addr, err)
			return
		}
		log.Printf("[%s] connected to %s (peer %s)", g.Cfg.Name, addr, p.ID)
	}()
}

func (g *Group) Stop() {
	if g.Watcher != nil {
		g.Watcher.Close()
	}
	if g.Discovery != nil {
		g.Discovery.Stop()
	}
	for _, p := range g.Engine.ListPeers() {
		if p.Conn != nil {
			p.Conn.Close()
		}
	}
}

func (a *App) Approve(syncName, folder string) error {
	g := a.FindGroup(syncName)
	if g == nil {
		return fmt.Errorf("unknown sync group %q", syncName)
	}
	return g.Engine.ApproveFolder(folder)
}

func (a *App) Status() string {
	var lines []string
	for _, g := range a.groups {
		pending, _ := g.Engine.PendingFolders()
		lines = append(lines, fmt.Sprintf("[%s] path=%s peers=%d pending=%v direction=%s role=%s approval=%s",
			g.Cfg.Name, g.Cfg.LocalPath, g.Engine.PeerCount(), pending,
			g.Cfg.Direction, g.Cfg.Role, g.Cfg.Approval))
	}
	summary, _ := a.Store.StatusSummary()
	lines = append(lines, summary)
	return fmt.Sprintf("%s\npeer_id=%s", joinLines(lines), a.PeerID)
}

func joinLines(lines []string) string {
	out := ""
	for i, l := range lines {
		if i > 0 {
			out += "\n"
		}
		out += l
	}
	return out
}
