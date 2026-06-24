package config

import (
	"fmt"
	"os"

	"github.com/BurntSushi/toml"
)

type RelayServerConfig struct {
	Listen  string `toml:"listen"`
	Key     string `toml:"key"`
	LogFile string `toml:"log_file"`
}

func DefaultRelayServer() RelayServerConfig {
	return RelayServerConfig{
		Listen: "0.0.0.0:7750",
	}
}

func LoadRelayServer(path string) (*RelayServerConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read relay config: %w", err)
	}
	cfg := DefaultRelayServer()
	if err := toml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse relay config: %w", err)
	}
	if cfg.Listen == "" {
		cfg.Listen = DefaultRelayServer().Listen
	}
	if cfg.Key == "" {
		return nil, fmt.Errorf("key is required (shared secret for clients)")
	}
	return &cfg, nil
}
