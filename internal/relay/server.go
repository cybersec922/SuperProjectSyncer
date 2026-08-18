package relay

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"sync"
	"time"

	"github.com/superdata/superprojectsyncer/internal/transport"
)

type ServerConfig struct {
	Listen string
	Key    string
}

type Server struct {
	cfg   ServerConfig
	mu    sync.Mutex
	rooms map[string]*room
}

type room struct {
	key     string
	mu      sync.Mutex
	members map[string]*member
}

type member struct {
	peerID   string
	role     string
	direction string
	conn     net.Conn
	writeMu  sync.Mutex
}

func NewServer(cfg ServerConfig) *Server {
	return &Server{
		cfg:   cfg,
		rooms: make(map[string]*room),
	}
}

func (s *Server) Run(ctx context.Context) error {
	ln, err := transport.Listen(s.cfg.Listen, s.cfg.Key)
	if err != nil {
		return err
	}
	log.Printf("relay: listening on %s", s.cfg.Listen)

	go func() {
		<-ctx.Done()
		ln.Close()
	}()

	for {
		conn, err := ln.Accept()
		if err != nil {
			select {
			case <-ctx.Done():
				return nil
			default:
				log.Printf("relay: accept: %v", err)
				continue
			}
		}
		go s.handleConn(ctx, conn)
	}
}

func (s *Server) handleConn(ctx context.Context, conn net.Conn) {
	defer conn.Close()
	addr := conn.RemoteAddr().String()

	msgType, payload, err := ReadFrame(conn)
	if err != nil {
		return
	}
	if msgType != TypeAuth {
		return
	}
	var auth Auth
	if err := json.Unmarshal(payload, &auth); err != nil || auth.Key != s.cfg.Key {
		_ = WriteJSON(conn, TypeError, ErrorMsg{Message: "unauthorized"})
		return
	}
	if err := WriteFrame(conn, TypeAuthOK, nil); err != nil {
		return
	}

	msgType, payload, err = ReadFrame(conn)
	if err != nil {
		return
	}
	if msgType != TypeRegister {
		return
	}
	var reg Register
	if err := json.Unmarshal(payload, &reg); err != nil {
		return
	}
	if reg.SyncName == "" || reg.SyncKey == "" || reg.PeerID == "" {
		_ = WriteJSON(conn, TypeError, ErrorMsg{Message: "invalid register"})
		return
	}

	rk := RoomKey(reg.SyncName, reg.SyncKey)
	m := &member{
		peerID:    reg.PeerID,
		role:      reg.Role,
		direction: reg.Direction,
		conn:      conn,
	}

	s.mu.Lock()
	rm, ok := s.rooms[rk]
	if !ok {
		rm = &room{key: rk, members: make(map[string]*member)}
		s.rooms[rk] = rm
	}
	s.mu.Unlock()

	var existing []PeerJoin
	rm.mu.Lock()
	if old, dup := rm.members[reg.PeerID]; dup {
		// Close the stale socket only. Do not delete-by-id after this:
		// the old handleConn would otherwise remove *this* new member.
		log.Printf("relay: [%s] replacing session for peer %s", reg.SyncName, reg.PeerID)
		old.conn.Close()
		delete(rm.members, reg.PeerID)
	}
	for _, other := range rm.members {
		existing = append(existing, PeerJoin{PeerID: other.peerID, Role: other.role, Direction: other.direction})
	}
	rm.members[reg.PeerID] = m
	rm.mu.Unlock()

	if err := WriteFrame(conn, TypeRegisterOK, nil); err != nil {
		s.removeMemberIf(rk, m)
		return
	}
	for _, pj := range existing {
		if err := writeJSONLocked(m, TypePeerJoin, pj); err != nil {
			s.removeMemberIf(rk, m)
			return
		}
	}
	join := PeerJoin{PeerID: reg.PeerID, Role: reg.Role, Direction: reg.Direction}
	rm.mu.Lock()
	for _, other := range rm.members {
		if other.peerID == reg.PeerID {
			continue
		}
		_ = writeJSONLocked(other, TypePeerJoin, join)
	}
	rm.mu.Unlock()

	log.Printf("relay: [%s] peer %s joined room (%s) from %s", reg.SyncName, reg.PeerID, reg.Role, addr)

	s.readLoop(ctx, rk, m)
	if s.removeMemberIf(rk, m) {
		leave := PeerLeave{PeerID: reg.PeerID}
		rm.mu.Lock()
		for _, other := range rm.members {
			_ = writeJSONLocked(other, TypePeerLeave, leave)
		}
		rm.mu.Unlock()
		log.Printf("relay: [%s] peer %s left", reg.SyncName, reg.PeerID)
	}
}

func (s *Server) readLoop(ctx context.Context, rk string, m *member) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		_ = m.conn.SetReadDeadline(time.Now().Add(45 * time.Second))
		msgType, payload, err := ReadFrame(m.conn)
		if err != nil {
			log.Printf("relay: peer %s disconnected: %v", m.peerID, err)
			return
		}
		switch msgType {
		case TypeData:
			var d Data
			if err := json.Unmarshal(payload, &d); err != nil {
				continue
			}
			s.forward(rk, m.peerID, d)
		case TypePing:
			_ = writeFrameLocked(m, TypePing, nil)
		default:
			continue
		}
	}
}

func (s *Server) forward(rk, fromPeerID string, d Data) {
	s.mu.Lock()
	rm := s.rooms[rk]
	s.mu.Unlock()
	if rm == nil {
		return
	}
	rm.mu.Lock()
	defer rm.mu.Unlock()
	target, ok := rm.members[d.ToPeerID]
	if !ok || target.peerID == fromPeerID {
		return
	}
	if err := writeJSONLocked(target, TypeData, Data{
		FromPeerID: fromPeerID,
		MsgType:    d.MsgType,
		Payload:    d.Payload,
	}); err != nil {
		log.Printf("relay: forward to %s failed: %v", target.peerID, err)
		_ = target.conn.Close()
	}
}

// removeMemberIf drops m only if it is still the mapped session for that peer ID.
func (s *Server) removeMemberIf(rk string, m *member) bool {
	s.mu.Lock()
	rm := s.rooms[rk]
	s.mu.Unlock()
	if rm == nil {
		return false
	}
	rm.mu.Lock()
	cur, ok := rm.members[m.peerID]
	removed := ok && cur == m
	if removed {
		delete(rm.members, m.peerID)
	}
	empty := len(rm.members) == 0
	rm.mu.Unlock()
	if empty {
		s.mu.Lock()
		delete(s.rooms, rk)
		s.mu.Unlock()
	}
	return removed
}

func writeJSONLocked(m *member, msgType byte, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeFrameLocked(m, msgType, payload)
}

func writeFrameLocked(m *member, msgType byte, payload []byte) error {
	m.writeMu.Lock()
	defer m.writeMu.Unlock()
	return WriteFrame(m.conn, msgType, payload)
}

func (s *Server) Status() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	rooms := len(s.rooms)
	peers := 0
	for _, rm := range s.rooms {
		rm.mu.Lock()
		peers += len(rm.members)
		rm.mu.Unlock()
	}
	return fmt.Sprintf("relay: listen=%s rooms=%d peers=%d", s.cfg.Listen, rooms, peers)
}
