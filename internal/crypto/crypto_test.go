package crypto_test

import (
	"regexp"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/crypto"
)

func TestRoundTrip(t *testing.T) {
	e, err := crypto.New("test-master-key-32bytes!")
	if err != nil {
		t.Fatal(err)
	}

	secrets := []string{
		"sk-abc123",
		"my-api-key-with-fancy-chars!@#$%^&*()",
		"",
	}
	for _, s := range secrets {
		enc, err := e.Encrypt([]byte(s))
		if err != nil {
			t.Fatalf("encrypt(%q): %v", s, err)
		}
		dec, err := e.Decrypt(enc)
		if err != nil {
			t.Fatalf("decrypt(%q): %v", enc, err)
		}
		if string(dec) != s {
			t.Fatalf("roundtrip mismatch: got %q, want %q", string(dec), s)
		}
	}
}

func TestExchangeFingerprintIsStableAndSeparated(t *testing.T) {
	e1, _ := crypto.New("fingerprint-master-one")
	e2, _ := crypto.New("fingerprint-master-two")
	url := "https://api.example.com"
	secret := []byte("sk-private-value")
	got := e1.ExchangeFingerprint(url, secret)

	if got != e1.ExchangeFingerprint(url, secret) {
		t.Fatal("same identity produced different fingerprints")
	}
	if got == e1.ExchangeFingerprint(url+"/v2", secret) {
		t.Fatal("URL change did not change fingerprint")
	}
	if got == e1.ExchangeFingerprint(url, []byte("sk-other")) {
		t.Fatal("key change did not change fingerprint")
	}
	if got == e2.ExchangeFingerprint(url, secret) {
		t.Fatal("master key change did not change fingerprint")
	}
	if !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(got) {
		t.Fatalf("fingerprint is not lowercase SHA-256 hex: %q", got)
	}
	if strings.Contains(got, string(secret)) {
		t.Fatal("fingerprint contains plaintext secret")
	}
}

func TestEmptyKey(t *testing.T) {
	_, err := crypto.New("")
	if err == nil {
		t.Fatal("expected error for empty key")
	}
}

func TestTamperedCiphertext(t *testing.T) {
	e, err := crypto.New("another-key-1234567890")
	if err != nil {
		t.Fatal(err)
	}
	enc, err := e.Encrypt([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	// Tamper
	_, err = e.Decrypt(enc + "x")
	if err == nil {
		t.Fatal("expected error for tampered ciphertext")
	}
}

func TestDifferentKeys(t *testing.T) {
	e1, _ := crypto.New("key-one-1234567890123")
	e2, _ := crypto.New("key-two-1234567890123")

	enc, err := e1.Encrypt([]byte("hello"))
	if err != nil {
		t.Fatal(err)
	}
	// Decrypting with different key should fail.
	if _, err := e2.Decrypt(enc); err == nil {
		t.Fatal("expected error when decrypting with different key")
	}
}
