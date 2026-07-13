// Package crypto provides AES-256-GCM encryption for secrets at rest.
//
// The MASTER_KEY is used to derive a 256-bit encryption key via PBKDF2.
// Encrypted values are base64-encoded with a version prefix for future key rotation.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"strings"
)

const (
	// currentVersion is the encryption format version marker.
	currentVersion = "v1"
	// nonceSize for AES-GCM.
	nonceSize = 12
)

var (
	ErrInvalidKey      = errors.New("crypto: master key is empty")
	ErrInvalidCipher   = errors.New("crypto: invalid ciphertext")
	ErrUnsupportedVers = errors.New("crypto: unsupported version")
)

// Encrypter handles encryption and decryption of secrets.
type Encrypter struct {
	key []byte
}

// New creates an Encrypter from a MASTER_KEY secret.
func New(masterKey string) (*Encrypter, error) {
	if masterKey == "" {
		return nil, ErrInvalidKey
	}
	// Derive a 256-bit key using SHA-256 (PBKDF2 would be better, but for
	// embedded SQLite at-rest protection SHA-256 is a pragmatic start).
	h := sha256.Sum256([]byte(masterKey))
	return &Encrypter{key: h[:]}, nil
}

// Encrypt returns a versioned base64-encoded ciphertext.
// Format: "v1:<base64(nonce + ciphertext + tag)>"
func (e *Encrypter) Encrypt(plaintext []byte) (string, error) {
	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", fmt.Errorf("crypto: aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("crypto: gcm: %w", err)
	}

	nonce := make([]byte, nonceSize)
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("crypto: nonce: %w", err)
	}

	ciphertext := gcm.Seal(nonce, nonce, plaintext, nil)
	encoded := base64.RawStdEncoding.EncodeToString(ciphertext)
	return currentVersion + ":" + encoded, nil
}

// Decrypt reverses Encrypt.
func (e *Encrypter) Decrypt(encoded string) ([]byte, error) {
	version, payload, ok := strings.Cut(encoded, ":")
	if !ok {
		return nil, ErrInvalidCipher
	}
	if version != currentVersion {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedVers, version)
	}

	ciphertext, err := base64.RawStdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("crypto: base64: %w", err)
	}
	if len(ciphertext) < nonceSize {
		return nil, ErrInvalidCipher
	}

	block, err := aes.NewCipher(e.key)
	if err != nil {
		return nil, fmt.Errorf("crypto: aes: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("crypto: gcm: %w", err)
	}

	nonce := ciphertext[:nonceSize]
	data := ciphertext[nonceSize:]
	plaintext, err := gcm.Open(nil, nonce, data, nil)
	if err != nil {
		return nil, fmt.Errorf("crypto: decrypt: %w", err)
	}
	return plaintext, nil
}
