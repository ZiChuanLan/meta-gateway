package webdavsync

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"

	"golang.org/x/crypto/pbkdf2"
)

// envelopeUploadIterations: 210k PBKDF2-SHA256 rounds, inside the range the
// reader accepts (minIterations..maxIterations) and a reasonable upload-time cost.
const envelopeUploadIterations = 210_000

// EncryptEnvelope produces the same PBKDF2-SHA256 + AES-256-GCM envelope the
// download path decrypts, so a gateway-uploaded encrypted backup is restorable
// by this gateway itself with the backup unlock password.
func EncryptEnvelope(plaintext []byte, password string, iterations int) ([]byte, error) {
	if len(plaintext) == 0 {
		return nil, Error{Category: CategoryValidation, Message: "nothing to encrypt"}
	}
	if iterations < minIterations || iterations > maxIterations {
		return nil, Error{Category: CategoryValidation, Message: "invalid kdf iterations"}
	}
	salt := make([]byte, 16)
	iv := make([]byte, 12)
	if _, err := rand.Read(salt); err != nil {
		return nil, Error{Category: CategoryInternal, Message: "random salt failed"}
	}
	if _, err := rand.Read(iv); err != nil {
		return nil, Error{Category: CategoryInternal, Message: "random iv failed"}
	}
	key := pbkdf2.Key([]byte(password), salt, iterations, 32, sha256.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, Error{Category: CategoryInternal, Message: "cipher init failed"}
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, Error{Category: CategoryInternal, Message: "gcm init failed"}
	}
	ciphertext := gcm.Seal(nil, iv, plaintext, nil)
	envelope := EncryptedEnvelopeV1{
		Type:   envelopeType,
		V:      envelopeVersion,
		KDF:    envelopeKDF,
		Cipher: envelopeCipher,
		Iter:   iterations,
		Salt:   base64.StdEncoding.EncodeToString(salt),
		IV:     base64.StdEncoding.EncodeToString(iv),
		CT:     base64.StdEncoding.EncodeToString(ciphertext),
	}
	return json.Marshal(envelope)
}
