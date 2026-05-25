package security

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

const SecretKeyBytes = 32

var ErrInvalidCiphertext = errors.New("invalid secret ciphertext")

type SecretCipher struct {
	aead cipher.AEAD
}

func NewSecretCipher(key []byte) (*SecretCipher, error) {
	if len(key) != SecretKeyBytes {
		return nil, fmt.Errorf("secret encryption key must be exactly %d bytes", SecretKeyBytes)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("create secret cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create secret AEAD: %w", err)
	}
	return &SecretCipher{aead: aead}, nil
}

func (c *SecretCipher) Encrypt(plaintext string) (string, error) {
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate secret nonce: %w", err)
	}
	ciphertext := c.aead.Seal(nonce, nonce, []byte(plaintext), nil)
	return base64.RawURLEncoding.EncodeToString(ciphertext), nil
}

func (c *SecretCipher) Decrypt(encoded string) (string, error) {
	ciphertext, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(ciphertext) < c.aead.NonceSize() {
		return "", ErrInvalidCiphertext
	}
	plaintext, err := c.aead.Open(nil, ciphertext[:c.aead.NonceSize()], ciphertext[c.aead.NonceSize():], nil)
	if err != nil {
		return "", ErrInvalidCiphertext
	}
	return string(plaintext), nil
}
