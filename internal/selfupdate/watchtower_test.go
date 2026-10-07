package selfupdate

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// The tracked tag is the only thing the watchtower executor will install, and
// the console can only offer what that tag delivers. v4.0.0-beta.6 is why this
// matters: the image and the floating `beta` tag were pushed, no GitHub release
// was created for it, and the beta channel therefore kept offering v4.0.0-beta.5
// — a version the executor would never bring up while the tag pointed at beta.6.
func TestTrackingChannelDescribesWhatTheTagDelivers(t *testing.T) {
	// Status resolves the mode, which probes the watchtower companion. Point it
	// at a closed local port so the probe fails instantly instead of waiting on
	// DNS for a compose hostname that does not exist here.
	t.Setenv("WATCHTOWER_URL", "127.0.0.1:1")
	for _, test := range []struct{ tag, want string }{
		{"", ""},
		{"latest", "stable"},
		{"beta", "beta"},
		// A pinned tag names one build rather than a channel: the operator has
		// already decided which version this deployment installs.
		{"4.0.0-beta.6", ""},
		{"Beta", ""},
	} {
		t.Run(test.tag, func(t *testing.T) {
			t.Setenv("SELFUPDATE_TRACK_TAG", test.tag)
			if got := TrackingChannel(); got != test.want {
				t.Fatalf("TrackingChannel()=%q, want %q", got, test.want)
			}
			// Whatever the tag is, the status payload has to carry it: the
			// console decides whether to offer an install from these two fields.
			status := New("").Status()
			if status.TrackingTag != test.tag {
				t.Fatalf("status.TrackingTag=%q, want %q", status.TrackingTag, test.tag)
			}
		})
	}
}

// The apply endpoint refuses a target the tracked tag cannot deliver. The
// message used to name neither the tag nor the channel, so the only way to find
// out what to change was to read the compose file.
func TestTrackMismatchNamesBothSides(t *testing.T) {
	t.Setenv("SELFUPDATE_TRACK_TAG", "latest")
	err := trackMismatch("v4.0.0-beta.6")
	if !errors.Is(err, ErrTrackMismatch) {
		t.Fatalf("error does not wrap ErrTrackMismatch: %v", err)
	}
	message := err.Error()
	for _, want := range []string{"v4.0.0-beta.6", "beta build", "IMAGE_TAG=latest", "watchtower_channel_mismatch"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q does not mention %q", message, want)
		}
	}

	t.Setenv("SELFUPDATE_TRACK_TAG", "beta")
	// The beta channel carries prereleases and the stable releases after them,
	// so a stable target is allowed there — and only the running-version guard
	// stops a backwards move.
	if !TrackedTargetAllowed("v4.0.0") {
		t.Fatal("beta track rejects a stable release")
	}
	if !TrackedTargetAllowed("v4.0.0-beta.7") {
		t.Fatal("beta track rejects a prerelease")
	}
	// `latest` never carries a prerelease.
	t.Setenv("SELFUPDATE_TRACK_TAG", "latest")
	if TrackedTargetAllowed("v4.0.0-beta.7") {
		t.Fatal("latest track accepted a prerelease")
	}
	if !TrackedTargetAllowed("v4.0.0") {
		t.Fatal("latest track rejects a stable release")
	}
	// An unset tag is the compose default (`latest`), but an unknown value must
	// not silently become a channel.
	t.Setenv("SELFUPDATE_TRACK_TAG", "nightly")
	if TrackedTargetAllowed("v4.0.0") || TrackedTargetAllowed("v4.0.0-beta.7") {
		t.Fatal("unknown tracking tag accepted a target")
	}
}

// Another browser opens the console mid-update and has to know what the task is
// coming from: the tracked tag decides whether an exact version can be expected
// at all, and `from` is the only reference the tracked path can compare against.
func TestStatusCarriesTheTaskAndTheTrack(t *testing.T) {
	t.Setenv("WATCHTOWER_URL", "127.0.0.1:1")
	t.Setenv("SELFUPDATE_TRACK_TAG", "beta")
	s := New("")
	s.mu.Lock()
	s.target = "v4.0.0-beta.7"
	s.from = "v4.0.0-beta.6"
	s.phase = PhaseHandoff
	s.started = time.Now()
	s.mu.Unlock()

	status := s.Status()
	if status.From != "v4.0.0-beta.6" {
		t.Fatalf("from=%q, want the build the update started from", status.From)
	}
	if status.TrackingTag != "beta" || status.TrackingChannel != "beta" {
		t.Fatalf("track=%q/%q, want beta/beta", status.TrackingTag, status.TrackingChannel)
	}
	if status.Target != "v4.0.0-beta.7" || !status.Running || status.Phase != PhaseHandoff {
		t.Fatalf("task not reported: %+v", status)
	}
}
