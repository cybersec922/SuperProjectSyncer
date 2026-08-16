package relay

import (
	"errors"
	"io"
	"net"
	"sync"
	"time"
)

// virtualConn is a net.Conn that tunnels sync frames through the relay server.
type virtualConn struct {
	relay    *Client
	peerID   string
	addr     string
	mu       sync.Mutex
	readBuf  []byte
	incoming chan []byte
	closed   bool
	closeCh  chan struct{}
}

func newVirtualConn(c *Client, peerID string) *virtualConn {
	return &virtualConn{
		relay:    c,
		peerID:   peerID,
		addr:     PeerAddr(peerID),
		incoming: make(chan []byte, 256),
		closeCh:  make(chan struct{}),
	}
}

func (v *virtualConn) Read(b []byte) (int, error) {
	for {
		v.mu.Lock()
		if len(v.readBuf) > 0 {
			n := copy(b, v.readBuf)
			v.readBuf = v.readBuf[n:]
			v.mu.Unlock()
			return n, nil
		}
		if v.closed {
			v.mu.Unlock()
			return 0, io.EOF
		}
		v.mu.Unlock()

		select {
		case frame, ok := <-v.incoming:
			if !ok {
				return 0, io.EOF
			}
			v.mu.Lock()
			v.readBuf = append(v.readBuf, frame...)
			v.mu.Unlock()
		case <-v.closeCh:
			return 0, io.EOF
		}
	}
}

func (v *virtualConn) Write(b []byte) (int, error) {
	if len(b) < 5 {
		return 0, errors.New("relay: short write")
	}
	msgType := b[4]
	payload := append([]byte(nil), b[5:]...)
	if err := v.relay.sendData(v.peerID, msgType, payload); err != nil {
		return 0, err
	}
	return len(b), nil
}

func (v *virtualConn) deliver(msgType byte, payload []byte) {
	frame := PackSyncFrame(msgType, payload)
	select {
	case <-v.closeCh:
		return
	case v.incoming <- frame:
	}
}

func (v *virtualConn) Close() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.closed {
		return nil
	}
	v.closed = true
	close(v.closeCh)
	return nil
}

func (v *virtualConn) LocalAddr() net.Addr                { return relayAddr{v.addr} }
func (v *virtualConn) RemoteAddr() net.Addr               { return relayAddr{v.addr} }
func (v *virtualConn) SetDeadline(t time.Time) error      { return nil }
func (v *virtualConn) SetReadDeadline(t time.Time) error  { return nil }
func (v *virtualConn) SetWriteDeadline(t time.Time) error { return nil }

type relayAddr struct{ s string }

func (a relayAddr) Network() string { return "relay" }
func (a relayAddr) String() string  { return a.s }
