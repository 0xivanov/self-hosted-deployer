package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const defaultOutputFormat = "human"

var ErrConfigNotFound = errors.New("CLI config file not found")

type Config struct {
	ServerURL  string `json:"server_url"`
	AdminToken string `json:"admin_token"`
	Output     string `json:"output"`
}

func DefaultConfigPath() (string, error) {
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("find user config directory: %w", err)
	}
	return filepath.Join(dir, "deployer", "config.json"), nil
}

func LoadConfig(path string) (Config, error) {
	resolvedPath, err := resolveConfigPath(path)
	if err != nil {
		return Config{}, err
	}

	data, err := os.ReadFile(resolvedPath)
	if errors.Is(err, os.ErrNotExist) {
		return Config{}, fmt.Errorf("%w: run deployer login <server-url>", ErrConfigNotFound)
	}
	if err != nil {
		return Config{}, fmt.Errorf("read CLI config: %w", err)
	}

	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("parse CLI config: %w", err)
	}
	if cfg.Output == "" {
		cfg.Output = defaultOutputFormat
	}
	return cfg, nil
}

func SaveConfig(path string, cfg Config) error {
	resolvedPath, err := resolveConfigPath(path)
	if err != nil {
		return err
	}
	if cfg.Output == "" {
		cfg.Output = defaultOutputFormat
	}

	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("encode CLI config: %w", err)
	}
	data = append(data, '\n')

	if err := os.MkdirAll(filepath.Dir(resolvedPath), 0o700); err != nil {
		return fmt.Errorf("create CLI config directory: %w", err)
	}
	if err := os.WriteFile(resolvedPath, data, 0o600); err != nil {
		return fmt.Errorf("write CLI config: %w", err)
	}
	return nil
}

func resolveConfigPath(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	return DefaultConfigPath()
}
