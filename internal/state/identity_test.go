package state

import (
	"path/filepath"
	"testing"
)

func TestGetOrCreatePeerIDStable(t *testing.T) {
	dir := t.TempDir()
	s1, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	id1, err := s1.GetOrCreatePeerID()
	if err != nil {
		t.Fatal(err)
	}
	if id1 == "" {
		t.Fatal("empty peer id")
	}
	id1b, err := s1.GetOrCreatePeerID()
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id1b {
		t.Fatalf("same process: %s vs %s", id1, id1b)
	}
	if err := s1.Close(); err != nil {
		t.Fatal(err)
	}

	s2, err := Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	id2, err := s2.GetOrCreatePeerID()
	if err != nil {
		t.Fatal(err)
	}
	if id1 != id2 {
		t.Fatalf("after restart: %s vs %s", id1, id2)
	}
}
