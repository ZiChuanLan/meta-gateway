package store_test

import (
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

// Priority must survive every credential path. Update() rewrites the whole row,
// so a column missing from its SET list looks correct until an unrelated write
// (check-in, account sync, the admin drawer) silently resets it.
func TestCredentialPriorityRoundTrips(t *testing.T) {
	db := openTestDB(t)
	siteID, err := db.Site.Create(&domain.Site{Name: "s", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	id, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "api_key", SecretEnc: []byte("enc"),
		Status: domain.StatusEnabled, Priority: domain.CredentialPriorityPreferred,
	})
	if err != nil {
		t.Fatal(err)
	}

	created, err := db.Credential.GetByID(id)
	if err != nil || created == nil {
		t.Fatalf("get created: %+v err=%v", created, err)
	}
	if created.Priority != domain.CredentialPriorityPreferred {
		t.Fatalf("created priority = %d, want %d", created.Priority, domain.CredentialPriorityPreferred)
	}

	list, err := db.Credential.ListBySite(siteID)
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %+v err=%v", list, err)
	}
	if list[0].Priority != domain.CredentialPriorityPreferred {
		t.Fatalf("listed priority = %d, want %d", list[0].Priority, domain.CredentialPriorityPreferred)
	}

	// A write that does not touch the tier must not reset it.
	created.Status = domain.StatusDisabled
	if err := db.Credential.Update(created); err != nil {
		t.Fatal(err)
	}
	updated, err := db.Credential.GetByID(id)
	if err != nil || updated == nil {
		t.Fatalf("get updated: %+v err=%v", updated, err)
	}
	if updated.Priority != domain.CredentialPriorityPreferred || updated.Status != domain.StatusDisabled {
		t.Fatalf("after update priority=%d status=%q, want %d/%q",
			updated.Priority, updated.Status, domain.CredentialPriorityPreferred, domain.StatusDisabled)
	}
}

// The site key pool comes back in relay try order (priority DESC, then id), so
// a caller that only needs the single best key can take the head of the slice.
func TestEnabledAPIKeyPoolOrderedByPriority(t *testing.T) {
	db := openTestDB(t)
	siteID, err := db.Site.Create(&domain.Site{Name: "s", Status: domain.StatusEnabled})
	if err != nil {
		t.Fatal(err)
	}
	balanced, _ := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "api_key", SecretEnc: []byte("enc-1"),
		Status: domain.StatusEnabled,
	})
	backup, _ := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "api_key", SecretEnc: []byte("enc-2"),
		Status: domain.StatusEnabled, Priority: domain.CredentialPriorityBackup,
	})
	preferred, _ := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "api_key", SecretEnc: []byte("enc-3"),
		Status: domain.StatusEnabled, Priority: domain.CredentialPriorityPreferred,
	})

	pool, err := db.Credential.ListEnabledAPIKeysBySite(siteID)
	if err != nil {
		t.Fatal(err)
	}
	if len(pool) != 3 {
		t.Fatalf("pool size = %d, want 3", len(pool))
	}
	want := []int64{preferred, balanced, backup}
	for index, credential := range pool {
		if credential.ID != want[index] {
			t.Fatalf("pool[%d] = credential %d, want %d (order: priority DESC, id ASC)",
				index, credential.ID, want[index])
		}
	}
}
