package selfupdate

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeDocker serves a minimal Docker Engine API on a unix socket and records
// every call for assertions.
type fakeDocker struct {
	socket  string
	server  *http.Server
	calls   []string
	bodies  map[string]map[string]any // path → last decoded body
	failOn  map[string]int            // path → status to force
	inspect map[string]string         // container id → name
}

func newFakeDocker(t *testing.T) *fakeDocker {
	t.Helper()
	f := &fakeDocker{
		bodies:  map[string]map[string]any{},
		failOn:  map[string]int{},
		inspect: map[string]string{"self-id": "gw", "next-id": "gw-next"},
	}
	sock := filepath.Join(t.TempDir(), "docker.sock")
	lis, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/v1.41")
		body, _ := io.ReadAll(r.Body)
		f.calls = append(f.calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		if len(body) > 0 {
			var decoded map[string]any
			if json.Unmarshal(body, &decoded) == nil {
				f.bodies[r.URL.Path] = decoded
			}
		}
		if code, has := f.failOn[r.URL.Path]; has {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"message":"forced failure"}`))
			return
		}
		switch {
		case r.URL.Path == "/_ping":
			w.WriteHeader(200)
		case r.URL.Path == "/containers/self-id/json":
			w.Write([]byte(`{
				"Id": "self-id", "Name": "/gw",
				"Config": {"Image": "zichuanlan/meta-gateway:latest", "Env": ["ADMIN_TOKEN=x"], "Labels": {"app": "meta-gateway"}},
				"HostConfig": {"Binds": ["meta-gateway-data:/data"], "PortBindings": {"4100/tcp": [{"HostPort": "4100"}]}, "RestartPolicy": {"Name": "unless-stopped"}, "NetworkMode": "default"},
				"NetworkSettings": {"Networks": {"meta-gateway_default": {"Aliases": []}}}
			}`))
		case r.URL.Path == "/containers/next-id/json":
			w.Write([]byte(`{"Id": "next-id", "Name": "/gw-next", "Config": {"Image": "zichuanlan/meta-gateway:latest"}, "HostConfig": {}, "NetworkSettings": {}}`))
		case r.URL.Path == "/images/create":
			w.Write([]byte("{\"status\":\"Pulling from zichuanlan/meta-gateway\"}\n{\"status\":\"Download complete\"}\n"))
		case strings.HasPrefix(r.URL.Path, "/containers/create"):
			w.WriteHeader(201)
			id := "final-id"
			if r.URL.Query().Get("name") == "gw-next" {
				id = "next-id"
			}
			w.Write([]byte(`{"Id": "` + id + `"}`))
		default:
			w.WriteHeader(204)
		}
	})
	f.server = &http.Server{Handler: mux}
	go f.server.Serve(lis)
	t.Cleanup(func() { _ = f.server.Close() })
	f.socket = sock
	return f
}

func TestHandoffCreatesSuccessorWithoutPorts(t *testing.T) {
	f := newFakeDocker(t)
	t.Setenv("HOSTNAME", "self-id")
	svc := New(f.socket)
	if err := svc.Start(); err != nil {
		t.Fatal(err)
	}
	deadline := 100
	for svc.Status().Phase != PhaseHandoff && deadline > 0 {
		deadline--
		if svc.Status().Phase == PhaseFailed {
			t.Fatalf("handoff failed: %s", svc.Status().Error)
		}
		sleepBriefly()
	}
	if svc.Status().Phase != PhaseHandoff {
		t.Fatalf("phase=%s err=%q", svc.Status().Phase, svc.Status().Error)
	}

	createBody := f.bodies["/containers/create"]
	if createBody == nil {
		t.Fatal("successor was not created")
	}
	hostConfig := createBody["HostConfig"].(map[string]any)
	if _, has := hostConfig["PortBindings"]; has && hostConfig["PortBindings"] != nil {
		t.Fatalf("successor must not bind host ports: %v", hostConfig["PortBindings"])
	}
	if hostConfig["AutoRemove"] != true {
		t.Fatalf("successor AutoRemove: %v", hostConfig["AutoRemove"])
	}
	env := decodeStrings(createBody["Env"])
	if !contains(env, swapEnv+"=gw") || !contains(env, imageEnv+"=zichuanlan/meta-gateway:latest") {
		t.Fatalf("swap env missing: %v", env)
	}
	if !containsPrefix(env, portsEnv+"=") {
		t.Fatalf("ports env missing: %v", env)
	}
	if !callsInclude(f.calls, "POST", "/images/create") {
		t.Fatal("image was not pulled")
	}
}

func TestSwapRecreatesFinalAndRollsBack(t *testing.T) {
	f := newFakeDocker(t)
	t.Setenv("HOSTNAME", "next-id")
	client := NewClient(f.socket)
	ctx := context.Background()
	portsJSON := `{"4100/tcp":[{"HostPort":"4100"}]}`

	if err := runSwap(ctx, client, "gw", "zichuanlan/meta-gateway:latest", portsJSON, "unless-stopped"); err != nil {
		t.Fatalf("swap failed: %v", err)
	}
	// Order: stop old → remove old → create final (original name) → start final.
	stopIdx := callsIndex(f.calls, "POST", "/containers/gw/stop")
	removeIdx := callsIndex(f.calls, "DELETE", "/containers/gw")
	createIdx := callsIndex(f.calls, "POST", "/containers/create")
	startIdx := callsIndex(f.calls, "POST", "/containers/final-id/start")
	if stopIdx < 0 || removeIdx < 0 || createIdx < 0 || startIdx < 0 {
		t.Fatalf("calls=%v", f.calls)
	}
	if !(stopIdx < removeIdx && removeIdx < createIdx && createIdx < startIdx) {
		t.Fatalf("swap order wrong: %v", f.calls)
	}
	finalBody := f.bodies["/containers/create"]
	hostConfig := finalBody["HostConfig"].(map[string]any)
	if hostConfig["PortBindings"] == nil {
		t.Fatal("final container lost the port bindings")
	}
	env := decodeStrings(finalBody["Env"])
	if containsPrefix(env, swapEnv+"=") || containsPrefix(env, portsEnv+"=") {
		t.Fatalf("final container still carries swap env: %v", env)
	}
	if _, has := finalBody["Healthcheck"]; has {
		t.Fatal("final container must restore the image healthcheck")
	}
	policy := hostConfig["RestartPolicy"].(map[string]any)
	if policy["Name"] != "unless-stopped" {
		t.Fatalf("restart policy: %v", policy)
	}
}

func TestSwapFailureRollsBackOldContainer(t *testing.T) {
	f := newFakeDocker(t)
	// Force the final-container creation to fail.
	f.failOn["/containers/create"] = 500
	t.Setenv("HOSTNAME", "next-id")
	client := NewClient(f.socket)
	ctx := context.Background()

	err := runSwap(ctx, client, "gw", "zichuanlan/meta-gateway:latest", `{"4100/tcp":[{"HostPort":"4100"}]}`, "unless-stopped")
	if err == nil {
		t.Fatal("expected failure")
	}
	if !callsInclude(f.calls, "POST", "/containers/gw/start") {
		t.Fatalf("old container was not rolled back: %v", f.calls)
	}
}

// ---- helpers ----

func sleepBriefly() {
	time.Sleep(10 * time.Millisecond)
}

func callsInclude(calls []string, method, path string) bool {
	return callsIndex(calls, method, path) >= 0
}

func callsIndex(calls []string, method, path string) int {
	for i, call := range calls {
		if strings.HasPrefix(call, method+" "+path) {
			return i
		}
	}
	return -1
}

func decodeStrings(raw any) []string {
	list, _ := raw.([]any)
	out := make([]string, 0, len(list))
	for _, entry := range list {
		if str, ok := entry.(string); ok {
			out = append(out, str)
		}
	}
	return out
}

func containsPrefix(list []string, prefix string) bool {
	for _, entry := range list {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}

func contains(list []string, want string) bool {
	for _, entry := range list {
		if entry == want {
			return true
		}
	}
	return false
}
