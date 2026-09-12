// Package selfupdate implements the console's one-click container update.
//
// The gateway cannot safely restart its own container: stopping it kills the
// whole cgroup mid-flight. The update therefore hands off to a successor
// container that finishes the swap from OUTSIDE the dying cgroup:
//
//	old gateway (button click)
//	  1. pull the new image
//	  2. create "{name}-next": same env/binds/network, NO host ports, NO
//	     healthcheck, env META_SELFUPDATE_SWAP={name} + the original port
//	     bindings carried as JSON
//	  3. start "{name}-next" and respond to the console
//
//	"{name}-next" (fresh container from the new image, still alive)
//	  4. stop + remove the old container (frees the name and host ports)
//	  5. create the final container: original name, original port bindings,
//	     no swap env, image healthcheck restored
//	  6. start the final container — a different cgroup, fully independent
//	  7. remove itself (auto-remove on exit)
//
// Any failure before step 6 restarts the old container (rollback); the old
// gateway keeps serving until the final container takes over, so the worst
// case downtime is the stop/start window of the final handoff.
//
// Security: the whole flow needs /var/run/docker.sock mounted into the
// container — roughly host-root power. The mount is opt-in in compose, the
// endpoints are admin-token gated, the target is limited to this project's
// own image, and apply actions land in the audit log.
package selfupdate

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	// DefaultSocket is where compose mounts the Docker socket for opt-in
	// self-update.
	DefaultSocket = "/var/run/docker.sock"
	// swapEnv marks the successor container and names the container it must
	// replace.
	swapEnv = "META_SELFUPDATE_SWAP"
	// portsEnv carries the original HostConfig.PortBindings as JSON.
	portsEnv = "META_SELFUPDATE_PORTS"
	// imageEnv carries the image reference to use for the final container.
	imageEnv = "META_SELFUPDATE_IMAGE"
	// policyEnv carries the original restart policy for the final container.
	policyEnv = "META_SELFUPDATE_POLICY"
	// DefaultAPIVersion is the Docker Engine API version prefix used on calls.
	DefaultAPIVersion = "v1.41"
)

// Client talks to the Docker Engine API over a unix socket.
type Client struct {
	socket string
	http   *http.Client
}

func NewClient(socket string) *Client {
	return &Client{
		socket: socket,
		http: &http.Client{
			Timeout: 10 * time.Minute,
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					return net.Dial("unix", socket)
				},
			},
		},
	}
}

func (c *Client) do(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, "http://docker"+DefaultAPIVersion+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	return c.http.Do(req)
}

// Ping reports whether the Docker API is reachable.
func (c *Client) Ping(ctx context.Context) error {
	resp, err := c.do(ctx, http.MethodGet, "/_ping", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("docker ping: status %d", resp.StatusCode)
	}
	return nil
}

// PullImage pulls ref and streams the daemon's progress lines to progress.
func (c *Client) PullImage(ctx context.Context, ref string, progress func(string)) error {
	resp, err := c.do(ctx, http.MethodPost,
		"/images/create?fromImage="+strings.SplitN(ref, ":", 2)[0]+
			"&tag="+strings.TrimSuffix(strings.SplitN(ref, ":", 2)[1]+":", ":"),
		nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("docker pull %s: status %d: %s", ref, resp.StatusCode, body)
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		if progress == nil {
			continue
		}
		var line struct {
			Status string `json:"status"`
			ID     string `json:"id"`
		}
		if json.Unmarshal(scanner.Bytes(), &line) == nil && line.Status != "" {
			progress(strings.TrimSpace(line.ID + " " + line.Status))
		}
	}
	return scanner.Err()
}

// Container is the slice of docker container inspect the updater needs.
type Container struct {
	ID   string
	Name string
	// Config is the container creation config (env, image, labels, ...).
	Config struct {
		Image        string            `json:"Image"`
		Env          []string          `json:"Env"`
		Labels       map[string]string `json:"Labels"`
		WorkingDir   string            `json:"WorkingDir"`
		Entrypoint   []string          `json:"Entrypoint"`
		Cmd          []string          `json:"Cmd"`
		ExposedPorts map[string]any    `json:"ExposedPorts"`
	} `json:"Config"`
	HostConfig struct {
		Binds         []string       `json:"Binds"`
		PortBindings  map[string]any `json:"PortBindings"`
		RestartPolicy struct {
			Name string `json:"Name"`
		} `json:"RestartPolicy"`
		NetworkMode string `json:"NetworkMode"`
		AutoRemove  bool   `json:"AutoRemove"`
		Init        *bool  `json:"Init"`
	} `json:"HostConfig"`
	NetworkSettings struct {
		Networks map[string]struct {
			Aliases []string `json:"Aliases"`
		} `json:"Networks"`
	} `json:"NetworkSettings"`
}

// InspectContainer fetches the full container inspect.
func (c *Client) InspectContainer(ctx context.Context, id string) (*Container, error) {
	resp, err := c.do(ctx, http.MethodGet, "/containers/"+id+"/json", nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("docker inspect %s: status %d: %s", id, resp.StatusCode, body)
	}
	var container Container
	if err := json.NewDecoder(resp.Body).Decode(&container); err != nil {
		return nil, err
	}
	container.Name = strings.TrimPrefix(container.Name, "/")
	return &container, nil
}

// CreateContainer creates a container from a raw create-config body and
// returns its id.
func (c *Client) CreateContainer(ctx context.Context, name string, config map[string]any) (string, error) {
	body, err := json.Marshal(config)
	if err != nil {
		return "", err
	}
	resp, err := c.do(ctx, http.MethodPost, "/containers/create?name="+name, strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		payload, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return "", fmt.Errorf("docker create %s: status %d: %s", name, resp.StatusCode, payload)
	}
	var out struct {
		ID string `json:"Id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", err
	}
	return out.ID, nil
}

func (c *Client) StopContainer(ctx context.Context, id string, timeoutSec int) error {
	resp, err := c.do(ctx, http.MethodPost,
		fmt.Sprintf("/containers/%s/stop?t=%d", id, timeoutSec), nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotModified {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("docker stop %s: status %d: %s", id, resp.StatusCode, body)
	}
	return nil
}

func (c *Client) StartContainer(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodPost, "/containers/"+id+"/start", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("docker start %s: status %d: %s", id, resp.StatusCode, body)
	}
	return nil
}

func (c *Client) RemoveContainer(ctx context.Context, id string) error {
	resp, err := c.do(ctx, http.MethodDelete, "/containers/"+id+"?force=true", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("docker remove %s: status %d: %s", id, resp.StatusCode, body)
	}
	return nil
}

// SocketAvailable reports whether the Docker socket exists and is a socket.
func SocketAvailable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.Mode()&os.ModeSocket != 0
}

// OwnContainerID returns the container id Docker injects as HOSTNAME.
func OwnContainerID() string {
	return strings.TrimSpace(os.Getenv("HOSTNAME"))
}
