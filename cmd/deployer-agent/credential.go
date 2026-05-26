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
	if err := writeSecretFile(path, token); err != nil {
		return fmt.Errorf("write agent credential: %w", err)
	}
	return nil
}

func writeWireGuardPrivateKey(path string, privateKey string) error {
	if strings.TrimSpace(privateKey) == "" {
		return errors.New("WireGuard private key is empty")
	}
	if err := writeSecretFile(path, privateKey); err != nil {
		return fmt.Errorf("write WireGuard private key: %w", err)
	}
	return nil
}

func writeSecretFile(path string, value string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create secret directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(strings.TrimSpace(value)+"\n"), 0o600); err != nil {
		return fmt.Errorf("write secret file: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return fmt.Errorf("restrict secret file: %w", err)
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

func readWireGuardPrivateKey(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read WireGuard private key: %w", err)
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", errors.New("WireGuard private key is empty")
	}
	return key, nil
}
