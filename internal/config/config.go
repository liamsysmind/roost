// Package config loads roost's TOML config file.
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

type Config struct {
	Auth   Auth   `toml:"auth"`
	Server Server `toml:"server"`
}

type Auth struct {
	PasswordHash  string `toml:"password_hash"`
	SessionSecret string `toml:"session_secret"`
}

type Server struct {
	Addr string `toml:"addr"`
}

func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.toml"
	}
	return filepath.Join(home, ".config", "roost", "config.toml")
}

func Load(path string) (*Config, error) {
	var c Config
	if _, err := toml.DecodeFile(path, &c); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	if c.Auth.PasswordHash == "" {
		return nil, fmt.Errorf("%s: auth.password_hash is required (run `roost setup`)", path)
	}
	if c.Auth.SessionSecret == "" {
		return nil, fmt.Errorf("%s: auth.session_secret is required (run `roost setup`)", path)
	}
	if c.Server.Addr == "" {
		c.Server.Addr = "127.0.0.1:8080"
	}
	return &c, nil
}

func Save(path string, c *Config) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	return toml.NewEncoder(f).Encode(c)
}
