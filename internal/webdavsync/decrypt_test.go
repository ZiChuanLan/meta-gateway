package webdavsync

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"testing"

	"golang.org/x/crypto/pbkdf2"
)

func TestDecryptEnvelopeRoundTrip(t *testing.T) {
	password := "backup-secret"
	plaintext := []byte(`{"version":"2.0","accounts":[],"apiCredentialProfiles":{"version":3,"profiles":[]}}`)
	envelope := mustEncryptEnvelope(t, password, plaintext, 1000)
	raw, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	parsed, ok := TryParseEncryptedEnvelope(raw)
	if !ok {
		t.Fatal("expected envelope parse")
	}
	got, err := DecryptEnvelope(parsed, password)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != string(plaintext) {
		t.Fatalf("plaintext mismatch")
	}
	if _, err := DecryptEnvelope(parsed, "wrong"); err == nil {
		t.Fatal("expected wrong password failure")
	}
}

func TestTryParseEncryptedEnvelopeRejectsPlainJSON(t *testing.T) {
	if _, ok := TryParseEncryptedEnvelope([]byte(`{"version":"2.0"}`)); ok {
		t.Fatal("plain json must not look like envelope")
	}
}

func mustEncryptEnvelope(t *testing.T, password string, plaintext []byte, iterations int) *EncryptedEnvelopeV1 {
	t.Helper()
	salt := make([]byte, 16)
	iv := make([]byte, 12)
	if _, err := rand.Read(salt); err != nil {
		t.Fatal(err)
	}
	if _, err := rand.Read(iv); err != nil {
		t.Fatal(err)
	}
	key := pbkdf2.Key([]byte(password), salt, iterations, 32, sha256.New)
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatal(err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	ct := gcm.Seal(nil, iv, plaintext, nil)
	return &EncryptedEnvelopeV1{
		Type:   envelopeType,
		V:      envelopeVersion,
		KDF:    envelopeKDF,
		Cipher: envelopeCipher,
		Iter:   iterations,
		Salt:   base64.StdEncoding.EncodeToString(salt),
		IV:     base64.StdEncoding.EncodeToString(iv),
		CT:     base64.StdEncoding.EncodeToString(ct),
	}
}
