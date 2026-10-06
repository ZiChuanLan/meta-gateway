package httpapi

import (
	"encoding/json"
	"testing"
)

// The console decides from these fields whether it may offer an install and what
// it may promise. In watchtower mode the executor updates the deployment's
// floating tag — not the release the console named — so the tag, and the channel
// that tag delivers, have to reach the browser. Until they did, the dialog could
// offer a beta on a `latest`-tracked deployment, get a refusal it did not
// explain, or (worse) watch for a version the executor would never bring up.
func TestSelfUpdateStatusExposesTheTrackedTag(t *testing.T) {
	// A closed local port, so the watchtower probe fails instantly instead of
	// waiting on DNS for a compose hostname that does not exist here.
	t.Setenv("WATCHTOWER_URL", "127.0.0.1:1")
	t.Setenv("SELFUPDATE_TRACK_TAG", "beta")
	e := newTeamTestEnv(t)

	var status struct {
		Running         bool   `json:"running"`
		Phase           string `json:"phase"`
		TrackingTag     string `json:"tracking_tag"`
		TrackingChannel string `json:"tracking_channel"`
		From            string `json:"from"`
	}
	if err := json.Unmarshal(e.admin("GET", "/admin/self-update", nil, 200), &status); err != nil {
		t.Fatal(err)
	}
	if status.TrackingTag != "beta" || status.TrackingChannel != "beta" {
		t.Fatalf("track=%q/%q, want beta/beta", status.TrackingTag, status.TrackingChannel)
	}
	if status.Running || status.Phase != "idle" {
		t.Fatalf("no task is running: %+v", status)
	}
	if status.From != "" {
		t.Fatalf("from=%q, want empty while idle", status.From)
	}

	var channel struct {
		Channel         string `json:"channel"`
		TrackingTag     string `json:"tracking_tag"`
		TrackingChannel string `json:"tracking_channel"`
	}
	if err := json.Unmarshal(e.admin("GET", "/admin/update-channel", nil, 200), &channel); err != nil {
		t.Fatal(err)
	}
	// The preference and the deployment's tag are two different facts; the
	// settings panel needs both to explain why a cross-track choice cannot be
	// installed.
	if channel.Channel != "stable" || channel.TrackingTag != "beta" || channel.TrackingChannel != "beta" {
		t.Fatalf("update-channel: %+v", channel)
	}
}
