package store_test

import "testing"

func TestAdminTOTPStoreLifecycle(t *testing.T) {
	db := openTestDB(t)

	state, err := db.AdminTOTP.Get()
	if err != nil {
		t.Fatal(err)
	}
	if state.Enabled || state.SecretEncrypted != "" {
		t.Fatalf("fresh state = %+v", state)
	}

	if err := db.AdminTOTP.SetSecret("v2:encrypted-secret"); err != nil {
		t.Fatal(err)
	}
	state, _ = db.AdminTOTP.Get()
	if state.SecretEncrypted != "v2:encrypted-secret" || state.Enabled {
		t.Fatalf("after set secret = %+v", state)
	}

	if err := db.AdminTOTP.SetEnabled(true); err != nil {
		t.Fatal(err)
	}
	state, _ = db.AdminTOTP.Get()
	if !state.Enabled {
		t.Fatal("should be enabled")
	}

	if err := db.AdminTOTP.Clear(); err != nil {
		t.Fatal(err)
	}
	state, _ = db.AdminTOTP.Get()
	if state.Enabled || state.SecretEncrypted != "" {
		t.Fatalf("after clear = %+v", state)
	}
}
