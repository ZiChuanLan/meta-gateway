package store_test

import (
	"bytes"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

// checkinFixture creates a site with one check-in credential (schedule on) and
// returns the ids. The credential is what the daily job runs with; it lives on
// the site, not on the channel, which is the whole reason a deleted connection
// used to keep checking in.
func checkinFixture(t *testing.T, db *store.DB, platform string) (siteID, credentialID int64) {
	t.Helper()
	siteID, err := db.Site.Create(&domain.Site{
		Name: "checkin-" + platform, BaseURL: "https://" + platform + ".example",
		Platform: platform, Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	credentialID, err = db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "session", SecretEnc: []byte("cipher"), Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Credential.SetCheckinEnabled(credentialID, true); err != nil {
		t.Fatal(err)
	}
	return siteID, credentialID
}

func createChannel(t *testing.T, db *store.DB, siteID, credentialID int64) int64 {
	t.Helper()
	channelID, err := db.Channel.Create(&domain.Channel{
		SiteID: &siteID, CredentialID: &credentialID, Name: "conn",
		Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	return channelID
}

func checkinEnabled(t *testing.T, db *store.DB, credentialID int64) bool {
	t.Helper()
	credential, err := db.Credential.GetByID(credentialID)
	if err != nil || credential == nil {
		t.Fatalf("credential %d: %+v err=%v", credentialID, credential, err)
	}
	return credential.CheckinEnabled
}

// Deleting the site's last connection must turn its check-in schedule off: the
// schedule belongs to the connection, and the credential it runs with outlives
// the channel row. Before this, the daily job kept calling an upstream the
// operator had just removed and the check-in page filled with transport errors.
//
// The site, the credential and the log history must survive — re-adding a
// connection to the same base URL reuses them, and the history is the only
// record of what the deleted connection did.
func TestChannelDeleteTurnsOffLastSiteCheckin(t *testing.T) {
	db := openTestDB(t)
	siteID, credentialID := checkinFixture(t, db, "new-api")
	channelID := createChannel(t, db, siteID, credentialID)
	// Prime the hot-path cache: clearing only the durable flag would leave
	// queued scheduled runs seeing the stale enabled value.
	credential, err := db.Credential.GetByID(credentialID)
	if err != nil || credential == nil || !credential.CheckinEnabled {
		t.Fatalf("prime credential cache: %+v err=%v", credential, err)
	}
	if err := db.CheckinLog.Create(&domain.CheckinLog{
		SiteID: siteID, CredentialID: credentialID, Source: "scheduled",
		Status: "failed", Category: "transport", Message: "could not reach upstream",
	}); err != nil {
		t.Fatal(err)
	}

	if err := db.Channel.Delete(channelID); err != nil {
		t.Fatal(err)
	}

	if checkinEnabled(t, db, credentialID) {
		t.Fatal("check-in schedule survived the last connection")
	}
	retained, err := db.Credential.GetByID(credentialID)
	if err != nil || retained == nil || !bytes.Equal(retained.SecretEnc, credential.SecretEnc) {
		t.Fatalf("stored credential changed on delete: %+v err=%v", retained, err)
	}
	enabled, err := db.Credential.ListCheckinEnabled()
	if err != nil {
		t.Fatal(err)
	}
	if len(enabled) != 0 {
		t.Fatalf("scheduler still lists the orphaned credential: %+v", enabled)
	}
	// Reversible, not destructive: the site and its stored token are still
	// there for a connection re-added to the same base URL.
	if site, err := db.Site.GetByID(siteID); err != nil || site == nil {
		t.Fatalf("site removed with the connection: %+v err=%v", site, err)
	}
	logs, err := db.CheckinLog.List(store.CheckinLogFilter{SiteID: &siteID})
	if err != nil || len(logs) != 1 {
		t.Fatalf("check-in history lost: %+v err=%v", logs, err)
	}
	createChannel(t, db, siteID, credentialID)
	if checkinEnabled(t, db, credentialID) {
		t.Fatal("re-adding a connection unexpectedly re-enabled check-in")
	}
}

func TestChannelDeleteDisablesAllSiteCheckinCredentials(t *testing.T) {
	db := openTestDB(t)
	siteID, credentialID := checkinFixture(t, db, "new-api")
	channelID := createChannel(t, db, siteID, credentialID)
	// The account used for check-in need not be the channel's bound API key.
	otherID, err := db.Credential.Create(&domain.Credential{
		SiteID: siteID, Kind: "access_token", SecretEnc: []byte("other-cipher"),
		Status: domain.StatusEnabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Credential.SetCheckinEnabled(otherID, true); err != nil {
		t.Fatal(err)
	}
	if !checkinEnabled(t, db, otherID) {
		t.Fatal("second credential schedule was not enabled")
	}
	if err := db.Channel.Delete(channelID); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{credentialID, otherID} {
		if checkinEnabled(t, db, id) {
			t.Fatalf("credential %d schedule survived the last connection", id)
		}
	}
}

func TestChannelDeleteRollsBackCheckinCleanupOnFailure(t *testing.T) {
	db := openTestDB(t)
	siteID, credentialID := checkinFixture(t, db, "new-api")
	channelID := createChannel(t, db, siteID, credentialID)
	if _, err := db.Exec(`CREATE TRIGGER reject_checkin_cleanup BEFORE UPDATE OF checkin_enabled ON credentials
		BEGIN SELECT RAISE(ABORT, 'checkin cleanup failed'); END`); err != nil {
		t.Fatal(err)
	}
	if err := db.Channel.Delete(channelID); err == nil {
		t.Fatal("channel deletion succeeded despite failed schedule cleanup")
	}
	if channel, err := db.Channel.GetByID(channelID); err != nil || channel == nil {
		t.Fatalf("channel deletion was not rolled back: %+v err=%v", channel, err)
	}
	if !checkinEnabled(t, db, credentialID) {
		t.Fatal("failed deletion changed the schedule")
	}
}

// Another channel on the same site keeps the account in use, so the schedule
// stays on. This is the rule that makes the cleanup safe for multi-connection
// sites.
func TestChannelDeleteKeepsCheckinWhileSiteHasAnotherChannel(t *testing.T) {
	db := openTestDB(t)
	siteID, credentialID := checkinFixture(t, db, "new-api")
	first := createChannel(t, db, siteID, credentialID)
	createChannel(t, db, siteID, credentialID)

	if err := db.Channel.Delete(first); err != nil {
		t.Fatal(err)
	}

	if !checkinEnabled(t, db, credentialID) {
		t.Fatal("check-in schedule turned off while the site still has a connection")
	}
	enabled, err := db.Credential.ListCheckinEnabled()
	if err != nil || len(enabled) != 1 {
		t.Fatalf("scheduler list: %+v err=%v", enabled, err)
	}
}

// External check-in sites have no channel by design — "no channel" is their
// normal state, so the cleanup must not reach them even if a channel was ever
// attached to the same site.
func TestChannelDeleteKeepsExternalCheckinSchedule(t *testing.T) {
	db := openTestDB(t)
	siteID, credentialID := checkinFixture(t, db, "external-checkin")
	channelID := createChannel(t, db, siteID, credentialID)

	if err := db.Channel.Delete(channelID); err != nil {
		t.Fatal(err)
	}

	if !checkinEnabled(t, db, credentialID) {
		t.Fatal("external check-in schedule was turned off with the connection")
	}
}

// Existing deployments already carry the bad state (a channel deleted by the
// old code left checkin_enabled=1). The migration backfills the rule; the
// external family must survive it untouched.
func TestOrphanCheckinScheduleDisabledOnUpgrade(t *testing.T) {
	dir := t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	orphanSite, orphanCredential := checkinFixture(t, db, "new-api")
	orphanChannel := createChannel(t, db, orphanSite, orphanCredential)
	externalSite, externalCredential := checkinFixture(t, db, "external-checkin")
	// A site that still has a connection must keep its schedule.
	liveSite, liveCredential := checkinFixture(t, db, "new-api")
	createChannel(t, db, liveSite, liveCredential)
	if err := db.CheckinLog.Create(&domain.CheckinLog{
		SiteID: orphanSite, CredentialID: orphanCredential, Source: "scheduled",
		Status: "failed", Category: "transport",
	}); err != nil {
		t.Fatal(err)
	}
	// Simulate the pre-fix delete path: the channel goes, the schedule stays.
	if _, err := db.Exec(`DELETE FROM channels WHERE id = ?`, orphanChannel); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations WHERE name = '144_disable_orphan_checkin.sql'`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	upgraded, err := store.Open(dir)
	if err != nil {
		t.Fatalf("upgrade: %v", err)
	}
	defer upgraded.Close()

	if checkinEnabled(t, upgraded, orphanCredential) {
		t.Fatal("migration left the orphaned schedule on")
	}
	if !checkinEnabled(t, upgraded, liveCredential) {
		t.Fatal("migration turned off a schedule whose site still has a connection")
	}
	if !checkinEnabled(t, upgraded, externalCredential) {
		t.Fatal("migration turned off an external check-in schedule")
	}
	if site, err := upgraded.Site.GetByID(externalSite); err != nil || site == nil {
		t.Fatalf("external site removed: %+v err=%v", site, err)
	}
	logs, err := upgraded.CheckinLog.List(store.CheckinLogFilter{SiteID: &orphanSite})
	if err != nil || len(logs) != 1 {
		t.Fatalf("check-in history lost on upgrade: %+v err=%v", logs, err)
	}
	if err := store.Migrate(upgraded.DB); err != nil {
		t.Fatalf("idempotent migration: %v", err)
	}
}
