package selfupdate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

func TestSwapRestoresRealOriginalOnReplacementFailures(t *testing.T) {
	for _, failure := range []string{"create", "start", "health", "cancel", ""} {
		t.Run(failure, func(t *testing.T) {
			names := map[string]string{"gw": "old-id"}
			running := map[string]bool{"old-id": true}
			var created map[string]any
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			client := NewClient("")
			client.http.Transport = pullTransport(func(r *http.Request) (*http.Response, error) {
				if err := r.Context().Err(); err != nil {
					return nil, err
				}
				path := r.URL.Path
				reply := func(code int, value any) (*http.Response, error) {
					b, _ := json.Marshal(value)
					return &http.Response{StatusCode: code, Body: io.NopCloser(strings.NewReader(string(b))), Header: make(http.Header)}, nil
				}
				if path == "/containers/create" {
					_ = json.NewDecoder(r.Body).Decode(&created)
					if failure == "cancel" {
						cancel()
						return nil, context.Canceled
					}
					if failure == "create" {
						return reply(500, map[string]string{"message": "cannot create"})
					}
					if names["gw"] != "" {
						t.Fatal("old name was not freed")
					}
					names["gw"] = "new-id"
					running["new-id"] = false
					return reply(201, map[string]string{"Id": "new-id"})
				}
				parts := strings.Split(strings.TrimPrefix(path, "/containers/"), "/")
				name := parts[0]
				id := names[name]
				if id == "" {
					id = name
				}
				if _, exists := running[id]; !exists {
					return reply(404, map[string]string{"message": "not found"})
				}
				action := ""
				if len(parts) > 1 {
					action = parts[1]
				}
				switch action {
				case "json":
					if id == "new-id" {
						health := "healthy"
						if failure == "health" {
							health = "unhealthy"
						}
						return reply(200, map[string]any{"State": map[string]any{"Running": running[id], "Health": map[string]string{"Status": health}}})
					}
					return reply(200, map[string]any{"Id": id, "Name": "/" + name,
						"Config":     map[string]any{"Image": "zichuanlan/meta-gateway:old", "User": "10001", "Env": []string{"HTTP_ADDR=:4100"}},
						"HostConfig": map[string]any{"Binds": []string{"data:/data"}, "Init": true, "GroupAdd": []string{"988"}, "Memory": 123456, "PortBindings": map[string]any{"4100/tcp": []map[string]string{{"HostPort": "4100"}}}},
						"State":      map[string]bool{"Running": running[id]}})
				case "stop":
					running[id] = false
				case "rename":
					to := r.URL.Query().Get("name")
					if names[to] != "" {
						return reply(409, nil)
					}
					delete(names, name)
					names[to] = id
				case "start":
					if id == "new-id" && failure == "start" {
						return reply(500, nil)
					}
					running[id] = true
				case "":
					if r.Method != http.MethodDelete {
						t.Fatalf("unexpected %s %s", r.Method, path)
					}
					delete(running, id)
					for n, v := range names {
						if v == id {
							delete(names, n)
						}
					}
				default:
					return nil, fmt.Errorf("unexpected %s", path)
				}
				return reply(204, nil)
			})
			err := runSwap(ctx, client, "gw", "zichuanlan/meta-gateway:new", "", "")
			if failure != "" {
				if err == nil || names["gw"] != "old-id" || !running["old-id"] {
					t.Fatalf("failed rollback: err=%v names=%v running=%v", err, names, running)
				}
				if _, ok := running["new-id"]; ok {
					t.Fatal("failed replacement retained")
				}
			} else {
				if err != nil || names["gw"] != "new-id" || !running["new-id"] {
					t.Fatalf("swap: %v %v", err, names)
				}
				if _, ok := running["old-id"]; ok {
					t.Fatal("old container not cleaned after health success")
				}
				host := created["HostConfig"].(map[string]any)
				if host["Init"] != true || host["Memory"] != float64(123456) || created["User"] != "10001" {
					t.Fatalf("deployment settings lost: %v", created)
				}
			}
		})
	}
}

func TestUpdateDeadlineReleasesStuckHandoff(t *testing.T) {
	s := New("")
	s.started = time.Now().Add(-17 * time.Minute)
	s.phase = PhaseHandoff
	s.mu.Lock()
	s.expireLocked()
	s.mu.Unlock()
	if s.phase != PhaseFailed || s.errStr == "" {
		t.Fatal("stuck handoff did not expire")
	}
}
