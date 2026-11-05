// Package config resolves the data directory and the stable instance ID.
package config

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"netchat/internal/osutil"
	"netchat/internal/protocol"
)

type Config struct {
	DataDir    string
	InstanceID string
	Name       string // machine display name
	Port       int    // LAN UDP+TCP port
	HTTPPort   int    // LAN HTTP port (default: Port; differs only for multi-instance testing)
	APIAddr    string // localhost control API listen address
	Group      string // multicast group
	AllIfaces  bool   // include virtual/VPN adapters in discovery
	Downloads  string // where pulled files are saved
}

type persisted struct {
	InstanceID string `json:"instanceId"`
}

// DefaultDataDir is %AppData%\netchat (Windows), ~/Library/Application Support/netchat, ~/.config/netchat.
func DefaultDataDir() string {
	if d, err := os.UserConfigDir(); err == nil {
		return filepath.Join(d, "netchat")
	}
	return ".netchat"
}

// Load fills defaults and loads/creates the persisted instance ID.
func Load(c Config) (Config, error) {
	if c.DataDir == "" {
		c.DataDir = DefaultDataDir()
	}
	if c.Port == 0 {
		c.Port = protocol.DefaultPort
	}
	if c.HTTPPort == 0 {
		c.HTTPPort = c.Port
	}
	if c.APIAddr == "" {
		c.APIAddr = fmt.Sprintf("127.0.0.1:%d", protocol.DefaultAPIPort)
	}
	if c.Group == "" {
		c.Group = protocol.DefaultGroup
	}
	if c.Name == "" {
		h, err := os.Hostname()
		if err != nil || h == "" {
			h = "unknown"
		}
		if osutil.IsWSL() {
			h += "-wsl" // WSL shares the Windows computer name; keep the two apart
		}
		c.Name = h
	}
	if c.Downloads == "" {
		if home, err := os.UserHomeDir(); err == nil {
			c.Downloads = filepath.Join(home, "Downloads", "netchat")
		} else {
			c.Downloads = filepath.Join(c.DataDir, "downloads")
		}
	}
	if err := os.MkdirAll(c.DataDir, 0o755); err != nil {
		return c, err
	}

	path := filepath.Join(c.DataDir, "instance.json")
	var p persisted
	b, err := os.ReadFile(path)
	switch {
	case err == nil:
		if err := json.Unmarshal(b, &p); err != nil {
			return c, fmt.Errorf("parse %s: %w", path, err)
		}
	case errors.Is(err, os.ErrNotExist):
	default:
		return c, err
	}
	if p.InstanceID == "" {
		p.InstanceID = RandomID(16)
		b, _ := json.MarshalIndent(p, "", "  ")
		if err := os.WriteFile(path, b, 0o644); err != nil {
			return c, err
		}
	}
	c.InstanceID = p.InstanceID
	return c, nil
}

// RandomID returns n random bytes hex-encoded.
func RandomID(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b)
}
