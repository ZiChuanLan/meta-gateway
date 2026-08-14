package totp_test

import (
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/totp"
)

func TestCodeAndVerify(t *testing.T) {
	secret, err := totp.NewSecret()
	if err != nil {
		t.Fatal(err)
	}
	if len(secret) != 32 {
		t.Fatalf("secret length = %d, want 32", len(secret))
	}
	now := time.Now()
	code, err := totp.Code(secret, now)
	if err != nil {
		t.Fatal(err)
	}
	if len(code) != 6 {
		t.Fatalf("code length = %d", len(code))
	}
	if !totp.Verify(secret, code, now) {
		t.Fatal("code should verify")
	}
	// ±1 step tolerance.
	if !totp.Verify(secret, code, now.Add(30*time.Second)) {
		t.Fatal("code should verify at +1 step")
	}
	if !totp.Verify(secret, code, now.Add(-30*time.Second)) {
		t.Fatal("code should verify at -1 step")
	}
	// Wrong code and out-of-window fail.
	if totp.Verify(secret, "000000", now) {
		t.Fatal("wrong code must fail")
	}
	if totp.Verify(secret, code, now.Add(2*30*time.Second)) {
		t.Fatal("code at +2 steps must fail")
	}
	if totp.Verify(secret, "", now) {
		t.Fatal("empty code must fail")
	}
}

func TestDeterministicRFC6238Vector(t *testing.T) {
	// RFC 6238 test vector: secret "12345678901234567890" (ASCII → base32
	// GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ), T=59 → 94287082 (SHA1, 8 digits).
	// Our implementation is 6 digits, so check the SHA1 6-digit value at
	// T=59: 287082 (last 6 of 94287082).
	secret := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	code, err := totp.Code(secret, time.Unix(59, 0))
	if err != nil {
		t.Fatal(err)
	}
	if code != "287082" {
		t.Fatalf("code at T=59 = %s, want 287082", code)
	}
}

func TestURIShape(t *testing.T) {
	uri := totp.URI("MetaGateway", "admin", "ABCDEFGHIJKLMNOPQRSTUVWX234567")
	if uri != "otpauth://totp/MetaGateway:admin?secret=ABCDEFGHIJKLMNOPQRSTUVWX234567&issuer=MetaGateway&algorithm=SHA1&digits=6&period=30" {
		t.Fatalf("uri = %s", uri)
	}
}
