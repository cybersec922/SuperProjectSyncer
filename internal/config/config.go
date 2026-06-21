package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type Direction string

const (
	DirectionBidirectional Direction = "bidirectional"
	DirectionPush          Direction = "push"
	DirectionPull          Direction = "pull"
)

type Role string

const (
	RoleProvider Role = "provider"
	RoleConsumer Role = "consumer"
)

type Approval string

const (
	ApprovalAuto      Approval = "auto"
	ApprovalAskFolder Approval = "ask_folder"
)

type Global struct {
	Listen    string `toml:"listen"`
	DataDir   string `toml:"data_dir"`
	Discovery bool   `toml:"discovery"`
}

type Sync struct {
	Name      string    `toml:"name"`
	LocalPath string    `toml:"local_path"`
	Direction Direction `toml:"direction"`
	Role      Role      `toml:"role"`
	Approval  Approval  `toml:"approval"`
	Peers     []string  `toml:"peers"`
	SyncKey   string    `toml:"sync_key"`
	Ignore    []string  `toml:"ignore"`
	Discovery *bool     `toml:"discovery"`
}

type Config struct {
	Global Global `toml:"global"`
	Syncs  []Sync `toml:"sync"`
}

func DefaultGlobal() Global {
	return Global{
		Listen:    "0.0.0.0:7741",
		DataDir:   "~/.superprojectsyncer",
		Discovery: true,
	}
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	cfg := &Config{Global: DefaultGlobal()}
	if err := toml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	if err := cfg.expandPaths(); err != nil {
		return nil, err
	}
	return cfg, nil
}

func (c *Config) Validate() error {
	if c.Global.Listen == "" {
		c.Global.Listen = DefaultGlobal().Listen
	}
	if c.Global.DataDir == "" {
		c.Global.DataDir = DefaultGlobal().DataDir
	}
	if len(c.Syncs) == 0 {
		return fmt.Errorf("at least one [[sync]] section is required")
	}
	names := make(map[string]bool)
	for i, s := range c.Syncs {
		if s.Name == "" {
			return fmt.Errorf("sync[%d]: name is required", i)
		}
		if names[s.Name] {
			return fmt.Errorf("sync[%d]: duplicate name %q", i, s.Name)
		}
		names[s.Name] = true
		if s.LocalPath == "" {
			return fmt.Errorf("sync[%q]: local_path is required", s.Name)
		}
		if s.SyncKey == "" {
			return fmt.Errorf("sync[%q]: sync_key is required", s.Name)
		}
		if s.Direction == "" {
			c.Syncs[i].Direction = DirectionBidirectional
		}
		if err := validateDirection(s.Direction); err != nil {
			return fmt.Errorf("sync[%q]: %w", s.Name, err)
		}
		if s.Direction != DirectionBidirectional {
			if s.Role == "" {
				return fmt.Errorf("sync[%q]: role is required for one-way direction", s.Name)
			}
			if s.Role != RoleProvider && s.Role != RoleConsumer {
				return fmt.Errorf("sync[%q]: role must be provider or consumer", s.Name)
			}
		}
		if s.Approval == "" {
			c.Syncs[i].Approval = ApprovalAuto
		}
		if s.Approval != ApprovalAuto && s.Approval != ApprovalAskFolder {
			return fmt.Errorf("sync[%q]: approval must be auto or ask_folder", s.Name)
		}
	}
	return nil
}

func validateDirection(d Direction) error {
	switch d {
	case DirectionBidirectional, DirectionPush, DirectionPull:
		return nil
	default:
		return fmt.Errorf("direction must be bidirectional, push, or pull")
	}
}

func (c *Config) expandPaths() error {
	expanded, err := expandPath(c.Global.DataDir)
	if err != nil {
		return fmt.Errorf("global.data_dir: %w", err)
	}
	c.Global.DataDir = expanded
	for i, s := range c.Syncs {
		p, err := expandPath(s.LocalPath)
		if err != nil {
			return fmt.Errorf("sync[%q].local_path: %w", s.Name, err)
		}
		c.Syncs[i].LocalPath = filepath.Clean(p)
	}
	return nil
}

func expandPath(p string) (string, error) {
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, p[2:]), nil
	}
	if p == "~" {
		return os.UserHomeDir()
	}
	return p, nil
}

func (s *Sync) DiscoveryEnabled(global bool) bool {
	if s.Discovery != nil {
		return *s.Discovery
	}
	return global
}

func (s *Sync) CanSend() bool {
	switch s.Direction {
	case DirectionBidirectional:
		return true
	case DirectionPush:
		return s.Role == RoleProvider
	case DirectionPull:
		return s.Role == RoleProvider
	default:
		return false
	}
}

func (s *Sync) CanReceive() bool {
	switch s.Direction {
	case DirectionBidirectional:
		return true
	case DirectionPush:
		return s.Role == RoleConsumer
	case DirectionPull:
		return s.Role == RoleConsumer
	default:
		return false
	}
}

func (s *Sync) CanInitiatePull() bool {
	return s.Direction == DirectionPull && s.Role == RoleConsumer
}
