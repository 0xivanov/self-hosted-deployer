package security

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const RandomTokenBytes = 32

const (
	AdminTokenPrefix = "dep_admin"
	JoinTokenPrefix  = "dep_join"
	AgentTokenPrefix = "dep_agent"
)

var (
	ErrInvalidPrefix = errors.New("invalid token prefix")
	ErrEmptyKey      = errors.New("token hash key is required")
)

func NewToken(prefix string) (string, error) {
	if !validPrefix(prefix) {
		return "", fmt.Errorf("%w: %s", ErrInvalidPrefix, prefix)
	}

	raw := make([]byte, RandomTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}

	return prefix + "_" + base64.RawURLEncoding.EncodeToString(raw), nil
}

func HashToken(key []byte, token string) (string, error) {
	if len(key) == 0 {
		return "", ErrEmptyKey
	}

	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(token))
	return hex.EncodeToString(mac.Sum(nil)), nil
}

func CompareTokenHash(expectedHash, actualHash string) bool {
	return subtle.ConstantTimeCompare([]byte(expectedHash), []byte(actualHash)) == 1
}

func Prefix(token string) (string, error) {
	idx := strings.LastIndex(token, "_")
	if idx <= 0 {
		return "", ErrInvalidPrefix
	}
	prefix := token[:idx]
	if !validPrefix(prefix) {
		return "", ErrInvalidPrefix
	}
	return prefix, nil
}

func RedactToken(token string) string {
	if token == "" {
		return ""
	}
	prefix, err := Prefix(token)
	if err != nil {
		return "[REDACTED]"
	}
	if len(token) <= len(prefix)+9 {
		return prefix + "_[REDACTED]"
	}
	return prefix + "_" + token[len(prefix)+1:len(prefix)+5] + "...[REDACTED]"
}

func validPrefix(prefix string) bool {
	switch prefix {
	case AdminTokenPrefix, JoinTokenPrefix, AgentTokenPrefix:
		return true
	default:
		return false
	}
}
