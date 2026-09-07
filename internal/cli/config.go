package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultOutputFormat = "human"

var ErrConfigNotFound = errors.New("CLI config file not found")

type Config struct {
	ServerURL      string              `json:"server_url"`
	AdminToken     string              `json:"admin_token"`
	Output         string              `json:"output"`
	Contexts       *map[string]Context `json:"contexts,omitempty"`
	CurrentContext string              `json:"current_context,omitempty"`
}

// Context identifies one independently managed control plane. AdminToken is
// kept for compatibility with the existing 0600 config file. New callers may
// use CredentialRef to resolve a token from env:NAME or file:/path instead.
type Context struct {
	ServerURL      string `json:"server_url"`
	AdminToken     string `json:"admin_token,omitempty"`
	CredentialRef  string `json:"credential_ref,omitempty"`
	EnvironmentID  string `json:"environment_id,omitempty"`
	CustomerLabel  string `json:"customer_label,omitempty"`
	ServerIdentity string `json:"server_identity,omitempty"`
}

var ErrContextNotFound = errors.New("CLI context not found")

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
	tmp, err := os.CreateTemp(filepath.Dir(resolvedPath), ".config.json-*")
	if err != nil {
		return fmt.Errorf("create temporary CLI config: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("protect temporary CLI config: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write CLI config: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync CLI config: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close CLI config: %w", err)
	}
	if err := os.Rename(tmpPath, resolvedPath); err != nil {
		return fmt.Errorf("replace CLI config: %w", err)
	}
	return nil
}

func (c Config) Context(name string) (Context, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Context{}, fmt.Errorf("context name is required")
	}
	if c.Contexts == nil {
		return Context{}, fmt.Errorf("%w: %q", ErrContextNotFound, name)
	}
	ctx, ok := (*c.Contexts)[name]
	if !ok {
		return Context{}, fmt.Errorf("%w: %q", ErrContextNotFound, name)
	}
	return ctx, nil
}

func ResolveContextCredential(ctx Context) (string, error) {
	if token := strings.TrimSpace(ctx.AdminToken); token != "" {
		return token, nil
	}
	ref := strings.TrimSpace(ctx.CredentialRef)
	if ref == "" {
		return "", errors.New("context has no admin credential")
	}
	switch {
	case strings.HasPrefix(ref, "env:"):
		name := strings.TrimSpace(strings.TrimPrefix(ref, "env:"))
		if name == "" {
			return "", errors.New("context credential reference has empty environment variable")
		}
		value := strings.TrimSpace(os.Getenv(name))
		if value == "" {
			return "", fmt.Errorf("context credential environment variable %q is empty", name)
		}
		return value, nil
	case strings.HasPrefix(ref, "file:"):
		path := strings.TrimSpace(strings.TrimPrefix(ref, "file:"))
		if path == "" {
			return "", errors.New("context credential reference has empty file path")
		}
		info, err := os.Stat(path)
		if err != nil {
			return "", fmt.Errorf("read context credential: %w", err)
		}
		if !info.Mode().IsRegular() {
			return "", errors.New("context credential must be a regular file")
		}
		if info.Size() > 64*1024 {
			return "", errors.New("context credential file is too large")
		}
		if info.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("context credential file %q is accessible by other users", path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("read context credential: %w", err)
		}
		value := strings.TrimSpace(string(data))
		if value == "" {
			return "", errors.New("context credential file is empty")
		}
		return value, nil
	default:
		return "", fmt.Errorf("unsupported context credential reference %q", ref)
	}
}

func resolveConfigPath(path string) (string, error) {
	if path != "" {
		return path, nil
	}
	return DefaultConfigPath()
}
