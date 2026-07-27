package webdavsync

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"

	"golang.org/x/crypto/pbkdf2"
)

const (
	envelopeType    = "all-api-hub-webdav-backup-encrypted"
	envelopeVersion = 1
	envelopeKDF     = "PBKDF2"
	envelopeCipher  = "AES-GCM"
	minIterations   = 1000
	maxIterations   = 5_000_000
)

// EncryptedEnvelopeV1 is the AAH WebDAV encrypted backup envelope.
type EncryptedEnvelopeV1 struct {
	Type   string `json:"type"`
	V      int    `json:"v"`
	KDF    string `json:"kdf"`
	Cipher string `json:"cipher"`
	Iter   int    `json:"iter"`
	Salt   string `json:"salt"`
	IV     string `json:"iv"`
	CT     string `json:"ct"`
}

// TryParseEncryptedEnvelope returns the envelope when content is recognized.
func TryParseEncryptedEnvelope(content []byte) (*EncryptedEnvelopeV1, bool) {
	var envelope EncryptedEnvelopeV1
	if err := json.Unmarshal(content, &envelope); err != nil {
		return nil, false
	}
	if envelope.Type != envelopeType || envelope.V != envelopeVersion {
		return nil, false
	}
	if envelope.KDF != envelopeKDF || envelope.Cipher != envelopeCipher {
		return nil, false
	}
	if envelope.Iter < minIterations || envelope.Iter > maxIterations {
		return nil, false
	}
	if strings.TrimSpace(envelope.Salt) == "" || strings.TrimSpace(envelope.IV) == "" || strings.TrimSpace(envelope.CT) == "" {
		return nil, false
	}
	return &envelope, true
}

// DecryptEnvelope decrypts AAH WebDAV backup envelope v1 (PBKDF2-SHA256 + AES-256-GCM).
func DecryptEnvelope(envelope *EncryptedEnvelopeV1, password string) ([]byte, error) {
	if envelope == nil {
		return nil, Error{Category: CategoryDecryptFailed, Message: "missing envelope"}
	}
	if strings.TrimSpace(password) == "" {
		return nil, Error{Category: CategoryDecryptFailed, Message: "backup unlock password required (not the WebDAV login password)"}
	}
	salt, err := base64.StdEncoding.DecodeString(envelope.Salt)
	if err != nil || len(salt) == 0 {
		return nil, Error{Category: CategoryDecryptFailed, Message: "invalid salt"}
	}
	iv, err := base64.StdEncoding.DecodeString(envelope.IV)
	if err != nil || len(iv) == 0 {
		return nil, Error{Category: CategoryDecryptFailed, Message: "invalid iv"}
	}
	ciphertext, err := base64.StdEncoding.DecodeString(envelope.CT)
	if err != nil || len(ciphertext) == 0 {
		return nil, Error{Category: CategoryDecryptFailed, Message: "invalid ciphertext"}
	}
	key := pbkdf2.Key([]byte(password), salt, envelope.Iter, 32, sha256.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, Error{Category: CategoryDecryptFailed, Message: "cipher init failed"}
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, Error{Category: CategoryDecryptFailed, Message: "gcm init failed"}
	}
	if len(iv) != gcm.NonceSize() {
		return nil, Error{Category: CategoryDecryptFailed, Message: "invalid iv length"}
	}
	plaintext, err := gcm.Open(nil, iv, ciphertext, nil)
	if err != nil {
		return nil, Error{Category: CategoryDecryptFailed, Message: "decrypt failed"}
	}
	return plaintext, nil
}
