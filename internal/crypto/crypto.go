// Package crypto provides AES-256-GCM encryption for secrets at rest.
//
// The MASTER_KEY is used to derive a 256-bit encryption key. New ciphertext uses
// PBKDF2 (v2). Legacy v1 ciphertext sealed with SHA-256(master) remains readable.
// Encrypted values are base64-encoded with a version prefix for future key rotation.
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

const (
	// currentVersion is the encryption format version marker for newly sealed secrets.
	currentVersion = "v2"
	// legacyVersion is the original SHA-256 derived key format.
	legacyVersion = "v1"
	// nonceSize for AES-GCM.
	nonceSize               = 12
	exchangeIdentityPurpose = "meta-gateway/exchange-identity/v1"
	// Fixed application salt for v2 PBKDF2 so the same MASTER_KEY decrypts across processes.
	masterKeySaltV2 = "meta-gateway/master-key/v2"
	// pbkdf2Iterations balances startup cost with offline brute-force resistance.
	pbkdf2Iterations = 120000
)

var (
	ErrInvalidKey      = errors.New("crypto: master key is empty")
	ErrInvalidCipher   = errors.New("crypto: invalid ciphertext")
	ErrUnsupportedVers = errors.New("crypto: unsupported version")
)

// Encrypter handles encryption and decryption of secrets.
type Encrypter struct {
	// key is the active sealing key (v2 PBKDF2).
	key []byte
	// keyV1 is retained so existing ciphertext remains readable after upgrade.
	keyV1 []byte
}

// ExchangeFingerprint returns a stable, purpose-separated identity for an
// imported base URL and API key. It is safe to persist but must not be exposed.
func (e *Encrypter) ExchangeFingerprint(normalizedBaseURL string, apiKey []byte) string {
	// Fingerprints must stay stable across cipher format upgrades, so they are
	// always derived from the legacy SHA-256 master material (keyV1).
	material := e.keyV1
	if len(material) == 0 {
		material = e.key
	}
	derive := hmac.New(sha256.New, material)
	_, _ = derive.Write([]byte(exchangeIdentityPurpose))
	identityKey := derive.Sum(nil)

	fingerprint := hmac.New(sha256.New, identityKey)
	_, _ = fingerprint.Write([]byte(normalizedBaseURL))
	_, _ = fingerprint.Write([]byte{0})
	_, _ = fingerprint.Write(apiKey)
	result := hex.EncodeToString(fingerprint.Sum(nil))
	for i := range identityKey {
		identityKey[i] = 0
	}
	return result
}

// New creates an Encrypter from a MASTER_KEY secret.
func New(masterKey string) (*Encrypter, error) {
	if masterKey == "" {
		return nil, ErrInvalidKey
	}
	legacy := sha256.Sum256([]byte(masterKey))
	modern := pbkdf2.Key([]byte(masterKey), []byte(masterKeySaltV2), pbkdf2Iterations, 32, sha256.New)
	return &Encrypter{
		key:   modern,
		keyV1: legacy[:],
	}, nil
}

// Encrypt returns a versioned base64-encoded ciphertext.
// Format: "v2:<base64(nonce + ciphertext + tag)>"
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
	var material []byte
	switch version {
	case currentVersion:
		material = e.key
	case legacyVersion:
		material = e.keyV1
		if len(material) == 0 {
			// Older Encrypter values constructed only with SHA-256 still work.
			material = e.key
		}
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedVers, version)
	}

	ciphertext, err := base64.RawStdEncoding.DecodeString(payload)
	if err != nil {
		return nil, fmt.Errorf("crypto: base64: %w", err)
	}
	if len(ciphertext) < nonceSize {
		return nil, ErrInvalidCipher
	}

	block, err := aes.NewCipher(material)
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
