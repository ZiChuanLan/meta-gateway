// Watchtower execution path for the one-click update: the compose companion
// service holds the Docker socket and sits completely idle until the console
// triggers it — HTTP API mode only, no periodic polls, so updates happen
// exclusively on an explicit operator action (stability first).
package selfupdate

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

// watchtowerURL/token resolve at call time so deployments can override them
// with WATCHTOWER_URL / WATCHTOWER_TOKEN.
func watchtowerURL() string {
	return strings.TrimRight(strings.TrimSpace(os.Getenv("WATCHTOWER_URL")), "/")
}

func watchtowerToken() string {
	return strings.TrimSpace(os.Getenv("WATCHTOWER_TOKEN"))
}

func defaults() (url, token string) {
	url = watchtowerURL()
	if url == "" {
		url = defaultWatchtowerURL
	}
	token = watchtowerToken()
	if token == "" {
		token = defaultWatchtowerToken
	}
	return url, token
}

// WatchtowerReachable reports whether the companion service answers on the
// compose network (short probe — it either runs or it does not).
func WatchtowerReachable() bool {
	url, _ := defaults()
	host := strings.TrimPrefix(url, "http://")
	host = strings.TrimPrefix(host, "https://")
	conn, err := net.DialTimeout("tcp", host, 400*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

// TriggerWatchtower asks the companion service to pull the new image and
// recreate the meta-gateway container. Watchtower returns as soon as the
// update is queued; the swap itself runs in the companion's cgroup, so this
// process never dies mid-handoff.
func TriggerWatchtower(ctx context.Context) error {
	url, token := defaults()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url+"/v1/update", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("watchtower update: status %d", resp.StatusCode)
	}
	return nil
}
