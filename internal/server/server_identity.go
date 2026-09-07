package server

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/0xivanov/self-hosted-deployer/internal/config"
)

const serverIdentityBytes = 32

// LoadServerIdentity returns the stable installation identity. An explicit
// value or file is preferred; otherwise a protected sidecar next to a file
// database is created once and reused across restarts.
func LoadServerIdentity(cfg config.ServerConfig) (string, error) {
	if value := strings.TrimSpace(cfg.ServerIdentity); value != "" {
		if strings.TrimSpace(cfg.ServerIdentityFile) != "" {
			return "", errors.New("set only one of DEPLOYER_SERVER_IDENTITY or DEPLOYER_SERVER_IDENTITY_FILE")
		}
		return validateIdentity("DEPLOYER_SERVER_IDENTITY", value)
	}
	path := strings.TrimSpace(cfg.ServerIdentityFile)
	if path == "" {
		path = sidecarPath(cfg.DatabaseURL)
	}
	if path == "" {
		return "", errors.New("server identity requires DEPLOYER_SERVER_IDENTITY or DEPLOYER_SERVER_IDENTITY_FILE when database is not file-backed")
	}
	return loadOrCreateIdentityFile(path)
}

func sidecarPath(databaseURL string) string {
	value := strings.TrimSpace(databaseURL)
	if strings.HasPrefix(value, "file:") {
		raw := strings.TrimPrefix(value, "file:")
		parsed, err := url.Parse("file:" + raw)
		if err != nil {
			return ""
		}
		query := parsed.Query()
		if strings.EqualFold(query.Get("mode"), "memory") {
			return ""
		}
		value = parsed.Path
		if value == "" && parsed.Opaque != "" {
			value, err = url.PathUnescape(parsed.Opaque)
			if err != nil {
				return ""
			}
		}
	} else if index := strings.IndexByte(value, '?'); index >= 0 {
		path, query, _ := strings.Cut(value, "?")
		values, _ := url.ParseQuery(query)
		if strings.EqualFold(values.Get("mode"), "memory") {
			return ""
		}
		value = path
	}
	if value == "" || value == ":memory:" {
		return ""
	}
	return value + ".identity"
}

func loadOrCreateIdentityFile(path string) (string, error) {
	if info, err := os.Lstat(path); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("server identity file %q must not be a symlink", path)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
			return "", fmt.Errorf("server identity file %q must be a regular owner-only file", path)
		}
		data, readErr := os.ReadFile(path)
		if readErr != nil {
			return "", fmt.Errorf("read server identity: %w", readErr)
		}
		return validateIdentity(path, string(data))
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("read server identity: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return "", fmt.Errorf("create server identity directory: %w", err)
	}
	bytes := make([]byte, serverIdentityBytes)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate server identity: %w", err)
	}
	value := hex.EncodeToString(bytes)
	tmp, err := os.CreateTemp(filepath.Dir(path), ".server-identity-*")
	if err != nil {
		return "", fmt.Errorf("create temporary server identity: %w", err)
	}
	tmpPath := tmp.Name()
	defer os.Remove(tmpPath)
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return "", fmt.Errorf("protect temporary server identity: %w", err)
	}
	if _, err := tmp.WriteString(value + "\n"); err != nil {
		tmp.Close()
		return "", fmt.Errorf("write server identity: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return "", fmt.Errorf("sync server identity: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close server identity: %w", err)
	}
	if err := os.Link(tmpPath, path); err != nil {
		if errors.Is(err, os.ErrExist) {
			return loadOrCreateIdentityFile(path)
		}
		return "", fmt.Errorf("publish server identity: %w", err)
	}
	return value, nil
}

func validateIdentity(path, raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" || strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("server identity file %q is empty or malformed", path)
	}
	return value, nil
}
