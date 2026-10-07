package selfupdate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// The shared volume is the whole protocol, so these tests are about the file
// contract: a heartbeat decides whether the executor exists, a request is
// written atomically, and the result is readable by the successor container.

func withUpdaterDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("COMPOSE_UPDATER_DIR", dir)
	return dir
}

func writeHeartbeat(t *testing.T, dir string, age time.Duration) {
	t.Helper()
	beat, err := json.Marshal(composeUpdaterInfo{PID: 42, Service: "meta-gateway", ProjectDir: "/opt/meta-gateway", StartedAt: time.Now().Unix()})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, ".ready")
	if err := os.WriteFile(path, beat, 0o644); err != nil {
		t.Fatal(err)
	}
	stamp := time.Now().Add(-age)
	if err := os.Chtimes(path, stamp, stamp); err != nil {
		t.Fatal(err)
	}
}

// An upgrade through the console replaces the image but never re-reads the
// deployment file, so the container ends up with the old environment (and without
// the compose-updater sidecar). The console has to say so: the alternative is an
// operator who never learns that the variables they added to .env are inert.
func TestDeploymentStepFollowsTheMarker(t *testing.T) {
	t.Setenv("HOSTNAME", "c0ffee123456")
	// No sidecar sharing the state volume, so the marker variable is the only
	// evidence available in these cases.
	withUpdaterDir(t)

	t.Setenv("SELFUPDATE_TRACK_TAG", "")
	if got := DeploymentStep(); got != "compose_recreate" {
		t.Fatalf("step=%q without the marker, want compose_recreate", got)
	}

	// The marker is present exactly when compose created this container from a
	// file that declares it — which is also when the environment is aligned.
	t.Setenv("SELFUPDATE_TRACK_TAG", "latest")
	if got := DeploymentStep(); got != "" {
		t.Fatalf("step=%q with the marker, want empty", got)
	}

	// Not in a container: there is no compose file to apply.
	t.Setenv("HOSTNAME", "")
	t.Setenv("SELFUPDATE_TRACK_TAG", "")
	if got := DeploymentStep(); got != "" {
		t.Fatalf("step=%q outside a container, want empty", got)
	}
}

// A deployment whose file has the updater service has applied its file, even if
// the marker variable is missing (a hand-written compose file, say). Asking it to
// apply the file again would be wrong.
func TestDeploymentStepAcceptsTheSidecarAsEvidence(t *testing.T) {
	t.Setenv("HOSTNAME", "c0ffee123456")
	t.Setenv("SELFUPDATE_TRACK_TAG", "")
	dir := withUpdaterDir(t)
	writeHeartbeat(t, dir, 0)
	if got := DeploymentStep(); got != "" {
		t.Fatalf("step=%q with a live sidecar, want empty", got)
	}
}

func TestComposeUpdaterAvailabilityFollowsTheHeartbeat(t *testing.T) {
	dir := withUpdaterDir(t)

	// No heartbeat: the volume may be mounted, but nothing is answering on it.
	if ComposeUpdaterAvailable() {
		t.Fatal("an empty directory was treated as a live updater")
	}
	if mode := New("").Mode(); mode == ModeCompose {
		t.Fatalf("mode=%s without a heartbeat", mode)
	}

	writeHeartbeat(t, dir, 0)
	if !ComposeUpdaterAvailable() {
		t.Fatal("a fresh heartbeat was not recognised")
	}
	if mode := New("").Mode(); mode != ModeCompose {
		t.Fatalf("mode=%s, want %s", mode, ModeCompose)
	}
	if got := ComposeUpdaterProjectDir(); got != "/opt/meta-gateway" {
		t.Fatalf("project dir=%q", got)
	}

	// Stale: the sidecar was removed from the compose file, or it died. Either
	// way the console must stop offering an update that would do nothing.
	writeHeartbeat(t, dir, composeUpdaterReadyTTL+time.Minute)
	if ComposeUpdaterAvailable() {
		t.Fatal("a stale heartbeat was treated as a live updater")
	}
}

func TestTriggerComposeUpdaterWritesTheRequestAtomically(t *testing.T) {
	dir := withUpdaterDir(t)
	writeHeartbeat(t, dir, 0)

	if err := TriggerComposeUpdater("v4.0.0", "v4.0.0-beta.8"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "request.json"))
	if err != nil {
		t.Fatal(err)
	}
	var request composeUpdaterRequest
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatalf("the request is not valid JSON: %v (%s)", err, raw)
	}
	if request.Target != "v4.0.0" || request.From != "v4.0.0-beta.8" || request.RequestedAt == 0 {
		t.Fatalf("request=%+v", request)
	}
	// The sidecar polls the path, so the temporary file must be gone: a leftover
	// would be re-read on the next loop.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".tmp" {
			t.Fatalf("temporary file left behind: %s", entry.Name())
		}
	}

	// A second click while the first is still queued must not stack requests.
	if err := TriggerComposeUpdater("v4.0.1", ""); !errors.Is(err, ErrComposeUpdaterBusy) {
		t.Fatalf("second trigger returned %v, want ErrComposeUpdaterBusy", err)
	}
}

func TestTriggerComposeUpdaterRefusesWithoutAnUpdater(t *testing.T) {
	withUpdaterDir(t)
	if err := TriggerComposeUpdater("v4.0.0", ""); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("trigger returned %v, want ErrUnavailable", err)
	}
}

func TestComposeUpdaterLastResult(t *testing.T) {
	dir := withUpdaterDir(t)
	if _, ok := ComposeUpdaterLastResult(); ok {
		t.Fatal("a result was reported without a result file")
	}
	payload := `{"target":"v4.0.0","exit_code":1,"started_at":10,"finished_at":20,"git":"ok","pull":"ok","up":"failed","log":"docker compose up failed"}`
	if err := os.WriteFile(filepath.Join(dir, "result.json"), []byte(payload), 0o644); err != nil {
		t.Fatal(err)
	}
	result, ok := ComposeUpdaterLastResult()
	if !ok {
		t.Fatal("result file was not read")
	}
	if result.ExitCode != 1 || result.Up != "failed" || result.Target != "v4.0.0" {
		t.Fatalf("result=%+v", result)
	}
	// The console renders this after the restart; a truncated or unreadable file
	// must not become a crash there.
	if err := os.WriteFile(filepath.Join(dir, "result.json"), []byte("{"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := ComposeUpdaterLastResult(); ok {
		t.Fatal("a malformed result file was accepted")
	}
}

// The compose updater follows the deployment's tag exactly like watchtower does,
// so the same guard applies: an unset tag is not a refusal (the executor installs
// the container's own tag either way), a pinned tag is.
func TestComposeModeUsesTheTrackedTargetGuard(t *testing.T) {
	dir := withUpdaterDir(t)
	writeHeartbeat(t, dir, 0)
	t.Setenv("SELFUPDATE_TRACK_TAG", "latest")

	service := New("")
	if mode := service.Mode(); mode != ModeCompose {
		t.Fatalf("mode=%s", mode)
	}
	if err := service.StartTarget("v4.0.0-beta.7"); !errors.Is(err, ErrTrackMismatch) {
		t.Fatalf("a prerelease on a latest deployment returned %v, want ErrTrackMismatch", err)
	}
	if err := service.StartTarget("v4.0.0"); err != nil {
		t.Fatalf("a stable target was refused: %v", err)
	}
	// The handoff is asynchronous by design — this process may be replaced at
	// any moment — so wait for the request rather than reading it immediately.
	request := filepath.Join(dir, "request.json")
	deadline := time.Now().Add(3 * time.Second)
	for {
		if _, err := os.Stat(request); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("no request was written to the shared volume")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
