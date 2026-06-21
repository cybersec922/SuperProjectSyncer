package protocol

import (
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
)

const Version = 1

const (
	TypeHello    byte = 1
	TypeManifest byte = 2
	TypeNeed     byte = 3
	TypeChunk    byte = 4
	TypeApplyOK  byte = 5
	TypeReject   byte = 6
	TypePing     byte = 7
)

type Hello struct {
	SyncName  string `json:"sync_name"`
	PeerID    string `json:"peer_id"`
	Direction string `json:"direction"`
	Role      string `json:"role"`
	Version   int    `json:"version"`
}

type FileEntry struct {
	Path  string `json:"path"`
	Hash  string `json:"hash"`
	Size  int64  `json:"size"`
	Mtime int64  `json:"mtime"`
}

type Manifest struct {
	SyncName string      `json:"sync_name"`
	Files    []FileEntry `json:"files"`
}

type Need struct {
	Paths []string `json:"paths"`
}

type Chunk struct {
	Path   string `json:"path"`
	Offset int64  `json:"offset"`
	Data   []byte `json:"data"`
	Final  bool   `json:"final"`
}

type Reject struct {
	Folder string `json:"folder"`
	Reason string `json:"reason"`
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
		return 0, nil, fmt.Errorf("invalid frame size %d", n)
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

func ReadJSON(r io.Reader, v any) (byte, error) {
	msgType, payload, err := ReadFrame(r)
	if err != nil {
		return 0, err
	}
	if err := json.Unmarshal(payload, v); err != nil {
		return msgType, err
	}
	return msgType, nil
}

func WritePing(w io.Writer) error {
	return WriteFrame(w, TypePing, nil)
}
