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

func TestTargetReached(t *testing.T) {
	cases := []struct {
		target, current string
		want           bool
	}{
		{"v4.7.6", "v4.7.6", true},
		// The journal names an older release than the one now running: a manual
		// pull or the sidecar overtook it, so the record is settled, not failed.
		{"v4.7.2", "v4.7.6", true},
		{"v4.7.2-beta.1", "v4.7.2", true},
		// A target the running build has not reached yet stays open.
		{"v4.8.0", "v4.7.6", false},
		{"v4.7.6", "v4.7.2-beta.1", false},
		// Builds that are not release tags cannot be compared.
		{"v4.7.2", "dev", false},
		{"v4.7.2", "v4.6.2-5-g0a66577", false},
		{"", "v4.7.6", false},
	}
	for _, tc := range cases {
		if got := targetReached(tc.target, tc.current); got != tc.want {
			t.Errorf("targetReached(%q, %q) = %v, want %v", tc.target, tc.current, got, tc.want)
		}
	}
}

func TestUpdateJournalIgnoresAFailureOvertakenByANewerBuild(t *testing.T) {
	old := buildinfo.Version
	buildinfo.Version = "v4.7.6"
	defer func() { buildinfo.Version = old }()

	dir := t.TempDir()
	s := NewWithDataDir("", dir)
	s.target = "v4.7.2"
	s.from = "v4.6.2-5-g0a66577"
	s.phase = PhaseFailed
	s.started = time.Now().Add(-24 * time.Hour)
	if err := s.persistLocked(); err != nil {
		t.Fatal(err)
	}

	resumed := NewWithDataDir("", dir)
	if resumed.phase != PhaseIdle {
		t.Fatalf("a failure below the running build still reports failure: %q (%q)", resumed.phase, resumed.errStr)
	}
	if resumed.errStr != "" {
		t.Fatalf("stale failure text survived: %q", resumed.errStr)
	}
}

func TestUpdateJournalKeepsFailureForAnUnreachedTarget(t *testing.T) {
	old := buildinfo.Version
	buildinfo.Version = "v4.7.6"
	defer func() { buildinfo.Version = old }()

	dir := t.TempDir()
	s := NewWithDataDir("", dir)
	s.target = "v4.8.0"
	s.phase = PhaseFailed
	s.started = time.Now().Add(-time.Hour)
	if err := s.persistLocked(); err != nil {
		t.Fatal(err)
	}
	if got := NewWithDataDir("", dir); got.phase != PhaseFailed {
		t.Fatalf("an unreached target must stay failed, got %q", got.phase)
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
