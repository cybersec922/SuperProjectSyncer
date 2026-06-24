package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/superdata/superprojectsyncer/internal/approval"
	"github.com/superdata/superprojectsyncer/internal/config"
	"github.com/superdata/superprojectsyncer/internal/discovery"
	"github.com/superdata/superprojectsyncer/internal/protocol"
	"github.com/superdata/superprojectsyncer/internal/relay"
	"github.com/superdata/superprojectsyncer/internal/state"
	syncengine "github.com/superdata/superprojectsyncer/internal/sync"
	"github.com/superdata/superprojectsyncer/internal/transport"
	"github.com/superdata/superprojectsyncer/internal/watcher"
)

type Group struct {
	Cfg         config.Sync
	Engine      *syncengine.Engine
	Approval    *approval.Queue
	Discovery   *discovery.Manager
	Watcher     *watcher.Watcher
	relayCancel context.CancelFunc
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
	if len(a.groups) > 0 {
		_ = a.Store.AppendActivity("", "info", "daemon stopped")
		_ = a.Store.ClearDaemonInfo()
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

	_ = a.Store.SetDaemonInfo(a.PeerID, os.Getpid(), a.Config.Global.Listen)
	_ = a.Store.AppendActivity("", "info", fmt.Sprintf("daemon started (peer_id=%s)", a.PeerID))
	go a.heartbeatLoop(ctx)

	if a.Config.Global.Relay != "" {
		for _, g := range a.groups {
			a.startRelay(ctx, g)
		}
	}

	go a.peerDialLoop(ctx)
	return nil
}

func (a *App) startRelay(ctx context.Context, g *Group) {
	relayCtx, cancel := context.WithCancel(ctx)
	g.relayCancel = cancel

	go func() {
		addr := a.Config.Global.Relay
		key := a.Config.Global.RelayKey
		backoff := 5 * time.Second
		for {
			select {
			case <-relayCtx.Done():
				return
			default:
			}
			client := relay.NewClient(relay.ClientConfig{
				Addr:      addr,
				Key:       key,
				SyncName:  g.Cfg.Name,
				SyncKey:   g.Cfg.SyncKey,
				PeerID:    a.PeerID,
				Role:      string(g.Cfg.Role),
				Direction: string(g.Cfg.Direction),
				OnPeer: func(peerID, peerAddr string, conn net.Conn) {
					for _, existing := range g.Engine.ListPeers() {
						if existing.ID == peerID {
							return
						}
					}
					if err := relay.ConnectPeer(g.Engine, peerID, peerAddr, conn); err != nil {
						log.Printf("[%s] relay peer %s: %v", g.Cfg.Name, peerID, err)
						return
					}
					log.Printf("[%s] relay connected to peer %s", g.Cfg.Name, peerID)
					_ = a.Store.AppendActivity(g.Cfg.Name, "info", fmt.Sprintf("relay peer %s", peerID))
				},
			})
			log.Printf("[%s] connecting to relay %s", g.Cfg.Name, addr)
			if err := client.Run(relayCtx); err != nil {
				select {
				case <-relayCtx.Done():
					return
				default:
					log.Printf("[%s] relay: %v (retry in %s)", g.Cfg.Name, err, backoff)
					_ = a.Store.AppendActivity(g.Cfg.Name, "warn", fmt.Sprintf("relay: %v", err))
				}
			}
			select {
			case <-relayCtx.Done():
				return
			case <-time.After(backoff):
			}
			if backoff < 30*time.Second {
				backoff += 5 * time.Second
			}
		}
	}()
}

func (a *App) heartbeatLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			_ = a.Store.TouchDaemon()
		}
	}
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
	_ = a.Store.AppendActivity(g.Cfg.Name, "info", fmt.Sprintf("inbound peer %s from %s", peerID, addr))
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
			_ = a.Store.AppendActivity(g.Cfg.Name, "warn", fmt.Sprintf("dial %s: %v", addr, err))
			return
		}
		log.Printf("[%s] connected to %s (peer %s)", g.Cfg.Name, addr, p.ID)
		_ = a.Store.AppendActivity(g.Cfg.Name, "info", fmt.Sprintf("connected to %s", addr))
	}()
}

func (g *Group) Stop() {
	if g.relayCancel != nil {
		g.relayCancel()
	}
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

func (a *App) Status(verbose bool) string {
	return StatusReport(a.Config, a.Store, verbose)
}

// StatusFromConfig reads live daemon state from SQLite (works while service runs).
func StatusFromConfig(cfg *config.Config, verbose bool) (string, error) {
	store, err := state.Open(cfg.Global.DataDir)
	if err != nil {
		return "", err
	}
	defer store.Close()
	return StatusReport(cfg, store, verbose), nil
}
