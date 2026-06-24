package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	syncengine "github.com/superdata/superprojectsyncer/internal/sync"
	"github.com/superdata/superprojectsyncer/internal/transport"
)

type PeerHandler func(peerID, addr string, conn net.Conn)

type Client struct {
	addr     string
	key      string
	syncName string
	syncKey  string
	peerID   string
	role     string
	direction string

	mu      sync.Mutex
	conn    net.Conn
	peers   map[string]*virtualConn
	onPeer  PeerHandler
}

type ClientConfig struct {
	Addr      string
	Key       string
	SyncName  string
	SyncKey   string
	PeerID    string
	Role      string
	Direction string
	OnPeer    PeerHandler
}

func NewClient(cfg ClientConfig) *Client {
	return &Client{
		addr:      cfg.Addr,
		key:       cfg.Key,
		syncName:  cfg.SyncName,
		syncKey:   cfg.SyncKey,
		peerID:    cfg.PeerID,
		role:      cfg.Role,
		direction: cfg.Direction,
		peers:     make(map[string]*virtualConn),
		onPeer:    cfg.OnPeer,
	}
}

func (c *Client) Run(ctx context.Context) error {
	conn, err := transport.Dial(c.addr, c.key, 15*time.Second)
	if err != nil {
		return fmt.Errorf("dial relay: %w", err)
	}
	c.mu.Lock()
	c.conn = conn
	c.peers = make(map[string]*virtualConn)
	c.mu.Unlock()
	defer c.closeAll()

	if err := WriteJSON(conn, TypeAuth, Auth{Key: c.key}); err != nil {
		return err
	}
	msgType, payload, err := ReadFrame(conn)
	if err != nil {
		return err
	}
	if msgType == TypeError {
		var e ErrorMsg
		_ = json.Unmarshal(payload, &e)
		return fmt.Errorf("relay auth: %s", e.Message)
	}
	if msgType != TypeAuthOK {
		return fmt.Errorf("relay: expected auth ok, got %d", msgType)
	}

	reg := Register{
		SyncName:  c.syncName,
		SyncKey:   c.syncKey,
		PeerID:    c.peerID,
		Role:      c.role,
		Direction: c.direction,
		Version:   1,
	}
	if err := WriteJSON(conn, TypeRegister, reg); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		msgType, payload, err = ReadFrame(conn)
		if err != nil {
			return err
		}
		switch msgType {
		case TypeRegisterOK:
			log.Printf("[%s] relay: registered at %s", c.syncName, c.addr)
		case TypePeerJoin:
			var pj PeerJoin
			if err := json.Unmarshal(payload, &pj); err != nil {
				continue
			}
			c.handlePeerJoin(pj)
		case TypePeerLeave:
			var pl PeerLeave
			if err := json.Unmarshal(payload, &pl); err != nil {
				continue
			}
			c.handlePeerLeave(pl.PeerID)
		case TypeData:
			var d Data
			if err := json.Unmarshal(payload, &d); err != nil {
				continue
			}
			c.handleData(d)
		case TypePing:
			continue
		case TypeError:
			var e ErrorMsg
			_ = json.Unmarshal(payload, &e)
			return fmt.Errorf("relay: %s", e.Message)
		}
	}
}

func (c *Client) handlePeerJoin(pj PeerJoin) {
	if pj.PeerID == c.peerID {
		return
	}
	c.mu.Lock()
	if _, ok := c.peers[pj.PeerID]; ok {
		c.mu.Unlock()
		return
	}
	vc := newVirtualConn(c, pj.PeerID)
	c.peers[pj.PeerID] = vc
	c.mu.Unlock()

	log.Printf("[%s] relay: peer joined %s (%s)", c.syncName, pj.PeerID, pj.Role)
	if c.onPeer != nil {
		c.onPeer(pj.PeerID, PeerAddr(pj.PeerID), vc)
	}
}

func (c *Client) handlePeerLeave(peerID string) {
	c.mu.Lock()
	vc, ok := c.peers[peerID]
	if ok {
		delete(c.peers, peerID)
	}
	c.mu.Unlock()
	if ok {
		_ = vc.Close()
		log.Printf("[%s] relay: peer left %s", c.syncName, peerID)
	}
}

func (c *Client) handleData(d Data) {
	from := d.FromPeerID
	if from == "" {
		from = d.ToPeerID // legacy
	}
	c.mu.Lock()
	vc, ok := c.peers[from]
	c.mu.Unlock()
	if !ok {
		return
	}
	vc.deliver(d.MsgType, d.Payload)
}

func (c *Client) sendData(toPeerID string, msgType byte, payload []byte) error {
	c.mu.Lock()
	conn := c.conn
	c.mu.Unlock()
	if conn == nil {
		return fmt.Errorf("relay: not connected")
	}
	return WriteJSON(conn, TypeData, Data{
		ToPeerID: toPeerID,
		MsgType:  msgType,
		Payload:  payload,
	})
}

func (c *Client) closeAll() {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, vc := range c.peers {
		_ = vc.Close()
	}
	c.peers = make(map[string]*virtualConn)
	if c.conn != nil {
		_ = c.conn.Close()
		c.conn = nil
	}
}

// ConnectPeer wires a relay virtual connection into the sync engine (same as a direct peer).
func ConnectPeer(engine *syncengine.Engine, peerID, addr string, conn net.Conn) error {
	p := &syncengine.Peer{ID: peerID, Addr: addr, Conn: conn}
	if err := engine.SendHello(p); err != nil {
		conn.Close()
		return err
	}
	engine.AddPeer(p)
	go engine.OnPeerConnected()
	go engine.HandleIncoming(p)
	return nil
}
