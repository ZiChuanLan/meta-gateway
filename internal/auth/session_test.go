package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestAdminSessionEnvelopeIsUnchanged(t *testing.T) {
	key := SessionSigningKey([]byte("session-test-master-key"), []string{"admin-token"})
	token, err := SignSessionToken(key, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !regexp.MustCompile(`^mg-sess\.\d+\.[A-Za-z0-9_-]+\.[A-Za-z0-9_-]+$`).MatchString(token) {
		t.Fatalf("admin envelope changed: %q", token)
	}
	if !VerifySessionToken(key, token) {
		t.Fatal("fresh admin session did not verify")
	}
}

func TestAdminSessionAcceptsExistingEnvelopes(t *testing.T) {
	// Construct the historical wire format independently of the signer.
	// Both the original expiry-only and newer nonce-bearing forms remain valid.
	key := []byte("session-compatibility-test-key")
	for _, nonce := range []string{"", ".historical-nonce"} {
		payload := fmt.Sprintf("%d%s", time.Now().Add(time.Hour).Unix(), nonce)
		mac := hmac.New(sha256.New, key)
		mac.Write([]byte("admin-session:" + payload))
		token := "mg-sess." + payload + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
		if !VerifySessionToken(key, token) {
			t.Fatalf("historical envelope rejected (nonce=%q)", nonce)
		}
	}
}

func TestAdminSessionRejectsExpiredTamperedAndForeignTokens(t *testing.T) {
	key := []byte("session-validation-test-key")
	token, err := SignSessionToken(key, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	expired, err := SignSessionToken(key, -time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	body := strings.TrimPrefix(token, SessionPrefix)
	for _, candidate := range []string{
		expired,
		token + "tampered",
		"mg-psess." + body,
		"mg-postate." + body,
	} {
		if VerifySessionToken(key, candidate) {
			t.Fatal("invalid or foreign session verified")
		}
	}
	if VerifySessionToken([]byte("different-test-key"), token) {
		t.Fatal("session verified with a different signing key")
	}
}
