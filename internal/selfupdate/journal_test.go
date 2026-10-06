package selfupdate

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/lan/meta-gateway/internal/buildinfo"
)

func TestUpdateJournalSurvivesRestartWithoutSecretsOrRenewedDeadline(t *testing.T) {
	dir := t.TempDir()
	service := NewWithDataDir("", dir)
	service.target = "v999.0.0"
	service.from = "v998.0.0"
	service.phase = PhasePulling
	service.started = time.Now().Add(-time.Minute).Truncate(time.Millisecond)
	service.errStr = "must-not-persist-secret"
	if err := service.persistLocked(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(service.journalPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "must-not-persist") {
		t.Fatal("journal retained error details")
	}
	resumed := NewWithDataDir("", dir)
	if resumed.phase != PhaseHandoff || resumed.target != service.target || !resumed.started.Equal(service.started) {
		t.Fatalf("lost task: %+v", resumed)
	}
	// The starting build survives too: without it a browser that opens the
	// console mid-update cannot tell a finished handoff from a running one.
	if resumed.from != service.from {
		t.Fatalf("lost the starting build: %+v", resumed)
	}
	resumed.started = time.Now().Add(-17 * time.Minute)
	if err = resumed.persistLocked(); err != nil {
		t.Fatal(err)
	}
	if next := NewWithDataDir("", dir); next.phase != PhaseFailed {
		t.Fatal("restart renewed a stale task")
	}
}

func TestUpdateJournalRecognizesTheTargetBuild(t *testing.T) {
	dir := t.TempDir()
	s := NewWithDataDir("", dir)
	s.target = buildinfo.Version
	s.phase = PhaseHandoff
	s.started = time.Now()
	if err := s.persistLocked(); err != nil {
		t.Fatal(err)
	}
	if got := NewWithDataDir("", dir); got.phase != PhaseIdle {
		t.Fatal("target build still reports running")
	}
}

func TestSuccessorDiagnosticsDoNotEchoSecrets(t *testing.T) {
	evidence := "Authorization: Bearer secret-value\nMASTER_KEY=private-key\npermission denied"
	hint := safeSuccessorHint(evidence)
	if !strings.Contains(hint, "permission denied") || strings.Contains(hint, "secret-value") || strings.Contains(hint, "private-key") {
		t.Fatalf("unsafe diagnostic hint: %q", hint)
	}
	if safeSuccessorHint("password=another-secret") != "" {
		t.Fatal("unknown output must not be exposed")
	}
}
