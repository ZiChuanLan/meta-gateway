package webdavsync

import (
	"testing"

	"github.com/lan/meta-gateway/internal/crypto"
	"github.com/lan/meta-gateway/internal/store"
)

type memoryWebDAVSettings struct{ row *store.WebDAVSettings }

func (m *memoryWebDAVSettings) Get() (*store.WebDAVSettings, error) {
	if m.row == nil {
		return &store.WebDAVSettings{CronExpr: "0 */6 * * *"}, nil
	}
	copyRow := *m.row
	return &copyRow, nil
}

func (m *memoryWebDAVSettings) Save(row *store.WebDAVSettings) error {
	copyRow := *row
	m.row = &copyRow
	return nil
}

func TestExplicitDisabledWebDAVOverrideDisarmsScheduler(t *testing.T) {
	enc, err := crypto.New("webdav-settings-test-key")
	if err != nil {
		t.Fatal(err)
	}
	settings := &memoryWebDAVSettings{}
	service := NewServiceWithSettings(Config{
		Enabled: true, URL: "https://dav.example", Username: "u", Password: "p",
		CronExpr: "0 */6 * * *",
	}, &Client{}, &fakeImporter{}, settings, enc)
	scheduler, err := NewScheduler(service, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.AttachScheduler(scheduler); err != nil {
		t.Fatal(err)
	}
	defer scheduler.Stop(t.Context())
	if !scheduler.Armed() || !service.Status().SchedulerArmed {
		t.Fatal("environment configuration should arm scheduler")
	}
	view, err := service.UpdateSettings(SettingsUpdate{
		Enabled:             false,
		ClearPassword:       true,
		ClearBackupPassword: true,
		CronExpr:            "0 */6 * * *",
	})
	if err != nil {
		t.Fatal(err)
	}
	if view.Source != "database" || view.Enabled || view.SchedulerArmed || scheduler.Armed() {
		t.Fatalf("explicit disabled override not applied: view=%+v armed=%v", view, scheduler.Armed())
	}
	if settings.row == nil || !settings.row.HasOverride {
		t.Fatal("settings save must persist HasOverride")
	}
}

func TestWebDAVSettingsRollbackWhenSchedulerStopped(t *testing.T) {
	enc, err := crypto.New("webdav-settings-rollback-key")
	if err != nil {
		t.Fatal(err)
	}
	settings := &memoryWebDAVSettings{}
	service := NewServiceWithSettings(Config{
		Enabled: true, URL: "https://dav.example", Username: "env-user", Password: "env-password",
		CronExpr: "0 */6 * * *",
	}, &Client{}, &fakeImporter{}, settings, enc)
	scheduler, err := NewScheduler(service, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.AttachScheduler(scheduler); err != nil {
		t.Fatal(err)
	}
	if err := scheduler.Stop(t.Context()); err != nil {
		t.Fatal(err)
	}

	if _, err := service.UpdateSettings(SettingsUpdate{
		Enabled:  true,
		URL:      "https://other.example",
		Username: "db-user",
		Password: "db-password",
		CronExpr: "5 * * * *",
	}); err == nil {
		t.Fatal("expected stopped scheduler to reject settings update")
	}
	if settings.row == nil || settings.row.HasOverride {
		t.Fatalf("durable settings were not rolled back: %+v", settings.row)
	}
	view, err := service.SettingsView()
	if err != nil {
		t.Fatal(err)
	}
	if view.Source != "env" || view.URL != "https://dav.example" || view.Username != "env-user" || view.SchedulerArmed {
		t.Fatalf("runtime settings were not rolled back: %+v", view)
	}
}
