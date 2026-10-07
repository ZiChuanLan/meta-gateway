// Compose updater execution path for the one-click update.
//
// The companion service (`tools/compose-updater/update.sh`) holds the Docker
// socket and the compose project directory, so one click can run the commands an
// operator would otherwise type by hand: `git pull --ff-only`,
// `docker compose pull`, `docker compose up -d --no-build --no-deps`.
//
// It exists because watchtower cannot do that part: watchtower recreates a
// container from the OLD container's inspect data, so `environment:` and `.env`
// changes never reach the replacement (containrrr/watchtower#233 — "it can't use
// docker-compose variables"). An env change therefore meant a hand-typed
// `docker compose up` forever, and the deployments that most need the update are
// the ones least able to follow that instruction.
//
// The gateway never sees the socket. The two containers share one small volume,
// which is the whole protocol:
//
//	gateway → updater   request.json   {"target":"v4.0.0", ...}
//	updater → gateway   result.json    {"exit_code":0, "log":"...", ...}
//	updater             .ready         heartbeat, refreshed every 30s
//
// A file needs no port, no token and no DNS name, so there is nothing to expose
// by accident; `.ready` is what distinguishes "the sidecar is there" from "the
// volume is mounted but the operator removed the service from the compose file".
package selfupdate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const (
	// defaultComposeUpdaterDir is the mount point the compose file uses. The
	// mount itself is the feature flag: no mount, no directory, no compose mode.
	defaultComposeUpdaterDir = "/update"
	// composeUpdaterReadyTTL bounds how stale a heartbeat may be. The sidecar
	// refreshes every 30s, so this tolerates a missed beat or two and a paused
	// container, while still noticing a service that was removed.
	composeUpdaterReadyTTL = 5 * time.Minute
)

// ErrComposeUpdaterBusy means a request is already waiting to be picked up.
var ErrComposeUpdaterBusy = errors.New("compose updater already has a pending request")

// composeUpdaterDir resolves the shared volume's mount point.
func composeUpdaterDir() string {
	if dir := os.Getenv("COMPOSE_UPDATER_DIR"); dir != "" {
		return dir
	}
	return defaultComposeUpdaterDir
}

// composeUpdaterInfo is the heartbeat the sidecar writes.
type composeUpdaterInfo struct {
	PID        int    `json:"pid"`
	Service    string `json:"service"`
	ProjectDir string `json:"project_dir"`
	StartedAt  int64  `json:"started_at"`
}

// ComposeUpdaterAvailable reports whether a live updater is sharing the volume.
func ComposeUpdaterAvailable() bool {
	_, err := ComposeUpdaterInfo()
	return err == nil
}

// ComposeUpdaterInfo reads the heartbeat. A missing or stale one means the
// executor is not there — the file is the only signal, so this is also what
// keeps a deployment that removed the sidecar from offering an update that would
// silently do nothing.
func ComposeUpdaterInfo() (composeUpdaterInfo, error) {
	path := filepath.Join(composeUpdaterDir(), ".ready")
	info, err := os.Stat(path)
	if err != nil {
		return composeUpdaterInfo{}, err
	}
	if time.Since(info.ModTime()) > composeUpdaterReadyTTL {
		return composeUpdaterInfo{}, fmt.Errorf("compose updater heartbeat is %s old", time.Since(info.ModTime()).Truncate(time.Second))
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return composeUpdaterInfo{}, err
	}
	var beat composeUpdaterInfo
	if err := json.Unmarshal(raw, &beat); err != nil {
		return composeUpdaterInfo{}, fmt.Errorf("compose updater heartbeat is unreadable: %w", err)
	}
	return beat, nil
}

// ComposeUpdaterProjectDir reports where the updater runs, for the console.
func ComposeUpdaterProjectDir() string {
	beat, err := ComposeUpdaterInfo()
	if err != nil {
		return ""
	}
	return beat.ProjectDir
}

// composeUpdaterRequest is what the gateway asks for. Target is recorded for the
// updater's log; the updater does NOT override the image tag with it — the
// deployment's own IMAGE_TAG decides which channel is installed, exactly as it
// does for a hand-typed `docker compose up`.
type composeUpdaterRequest struct {
	Target      string `json:"target,omitempty"`
	From        string `json:"from,omitempty"`
	RequestedAt int64  `json:"requested_at"`
}

// TriggerComposeUpdater hands the request to the sidecar and returns as soon as
// the file is on disk. The update itself runs in the companion container, so
// this process can be stopped by it at any point without losing the work.
func TriggerComposeUpdater(target, from string) error {
	dir := composeUpdaterDir()
	if !ComposeUpdaterAvailable() {
		return ErrUnavailable
	}
	request := filepath.Join(dir, "request.json")
	if _, err := os.Stat(request); err == nil {
		return ErrComposeUpdaterBusy
	}
	payload, err := json.Marshal(composeUpdaterRequest{Target: target, From: from, RequestedAt: time.Now().Unix()})
	if err != nil {
		return err
	}
	// Write-then-rename: the sidecar polls this path, and a half-written request
	// would be read as a malformed one.
	temporary := request + ".tmp"
	if err := os.WriteFile(temporary, payload, 0o644); err != nil {
		return err
	}
	return os.Rename(temporary, request)
}

// ComposeUpdaterResult is the outcome of the last run, written by the sidecar.
// The console reads it after the restart: without it a failed update leaves a
// container that is still on the old version and no explanation.
type ComposeUpdaterResult struct {
	Target     string `json:"target,omitempty"`
	ExitCode   int    `json:"exit_code"`
	StartedAt  int64  `json:"started_at,omitempty"`
	FinishedAt int64  `json:"finished_at,omitempty"`
	Pull       string `json:"pull,omitempty"`
	Up         string `json:"up,omitempty"`
	Log        string `json:"log,omitempty"`
}

// ComposeUpdaterLastResult reads the sidecar's result file, if any.
func ComposeUpdaterLastResult() (ComposeUpdaterResult, bool) {
	raw, err := os.ReadFile(filepath.Join(composeUpdaterDir(), "result.json"))
	if err != nil {
		return ComposeUpdaterResult{}, false
	}
	var result ComposeUpdaterResult
	if err := json.Unmarshal(raw, &result); err != nil {
		return ComposeUpdaterResult{}, false
	}
	return result, true
}
