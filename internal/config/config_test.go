package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadAndValidate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	content := `
[global]
listen = "127.0.0.1:7741"
data_dir = "` + filepath.ToSlash(dir) + `/data"

[[sync]]
name = "test-sync"
local_path = "` + filepath.ToSlash(dir) + `/syncroot"
direction = "push"
role = "provider"
approval = "ask_folder"
sync_key = "secret"
ignore = ["*.tmp"]
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Syncs[0].Name != "test-sync" {
		t.Fatalf("name = %q", cfg.Syncs[0].Name)
	}
	if !cfg.Syncs[0].CanSend() {
		t.Fatal("provider push should send")
	}
	if cfg.Syncs[0].CanReceive() {
		t.Fatal("provider push should not receive")
	}
}

func TestCanReceiveConsumer(t *testing.T) {
	s := Sync{Direction: DirectionPush, Role: RoleConsumer}
	if s.CanSend() {
		t.Fatal("consumer should not send on push")
	}
	if !s.CanReceive() {
		t.Fatal("consumer should receive on push")
	}
}
