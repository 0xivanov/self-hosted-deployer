package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func writeCredential(path string, token string) error {
	if strings.TrimSpace(token) == "" {
		return errors.New("join response did not include an agent credential")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create credential directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(token)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write agent credential: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("restrict agent credential: %w", err)
	}
	return nil
}

func readCredential(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read agent credential: %w", err)
	}
	token := strings.TrimSpace(string(data))
	if token == "" {
		return "", errors.New("agent credential is empty")
	}
	return token, nil
}
