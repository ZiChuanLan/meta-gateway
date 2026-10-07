package selfupdate

import "testing"

func TestWatchtowerTracksConfiguredImageOnly(t *testing.T) {
	for _, tc := range []struct {
		track, target string
		want          bool
	}{
		{"latest", "v1.2.3", true}, {"latest", "v1.3.0-beta.1", false}, {"beta", "v1.3.0-beta.2", true},
		{"beta", "v1.3.0", true}, {"1.2.3-beta.1", "v1.2.3-beta.2", false},
		// Unset is not "unknown": it is the container that predates the variable,
		// which the executor would update to its own tag anyway. A value that is
		// set but is not a channel (a pinned version, a typo) still refuses — see
		// watchtower_test.go.
		{"", "v1.2.3", true},
	} {
		t.Setenv("SELFUPDATE_TRACK_TAG", tc.track)
		if got := TrackedTargetAllowed(tc.target); got != tc.want {
			t.Errorf("%+v got %v", tc, got)
		}
	}
}

func TestHandoffUsesConfirmedBetaTag(t *testing.T) {
	f := newFakeDocker(t)
	t.Setenv("HOSTNAME", "self-id")
	s := New(f.socket)
	s.target = "v9.1.0-beta.2"
	s.handoff()
	body := f.bodies["/containers/create"]
	if body["Image"] != "zichuanlan/meta-gateway:9.1.0-beta.2" {
		t.Fatalf("image=%v", body["Image"])
	}
}
