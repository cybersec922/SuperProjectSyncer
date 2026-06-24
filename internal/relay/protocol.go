package relay

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

// Control message types on the relay TCP connection (separate from sync protocol).
const (
	TypeAuth       byte = 20
	TypeAuthOK     byte = 21
	TypeRegister   byte = 22
	TypeRegisterOK byte = 23
	TypePeerJoin   byte = 24
	TypePeerLeave  byte = 25
	TypeData       byte = 26
	TypeError      byte = 27
	TypePing       byte = 28
)

type Auth struct {
	Key string `json:"key"`
}

type Register struct {
	SyncName string `json:"sync_name"`
	SyncKey  string `json:"sync_key"`
	PeerID   string `json:"peer_id"`
	Role     string `json:"role"`
	Direction string `json:"direction"`
	Version  int    `json:"version"`
}

type PeerJoin struct {
	PeerID    string `json:"peer_id"`
	Role      string `json:"role"`
	Direction string `json:"direction"`
}

type PeerLeave struct {
	PeerID string `json:"peer_id"`
}

type Data struct {
	ToPeerID   string `json:"to_peer_id"`             // destination (client → server)
	FromPeerID string `json:"from_peer_id,omitempty"` // sender (server → client)
	MsgType    byte   `json:"msg_type"`
	Payload    []byte `json:"payload"`
}

type ErrorMsg struct {
	Message string `json:"message"`
}

func WriteFrame(w io.Writer, msgType byte, payload []byte) error {
	buf := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(buf[0:4], uint32(1+len(payload)))
	buf[4] = msgType
	copy(buf[5:], payload)
	_, err := w.Write(buf)
	return err
}

func ReadFrame(r io.Reader) (byte, []byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return 0, nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n == 0 || n > 64*1024*1024 {
		return 0, nil, fmt.Errorf("invalid relay frame size %d", n)
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return 0, nil, err
	}
	return body[0], body[1:], nil
}

func WriteJSON(w io.Writer, msgType byte, v any) error {
	payload, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return WriteFrame(w, msgType, payload)
}

func RoomKey(syncName, syncKey string) string {
	return syncName + "\x00" + syncKey
}

func PeerAddr(peerID string) string {
	return "relay:" + peerID
}

// PackSyncFrame builds the byte stream expected by protocol.ReadFrame on a virtual conn.
func PackSyncFrame(msgType byte, payload []byte) []byte {
	buf := make([]byte, 4+1+len(payload))
	binary.BigEndian.PutUint32(buf[0:4], uint32(1+len(payload)))
	buf[4] = msgType
	copy(buf[5:], payload)
	return buf
}
