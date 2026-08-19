// Command demo-plugin is a minimal reference implementation of the
// meta-gateway sidecar plugin protocol. Any HTTP service speaking the same
// contract works — the language does not matter.
//
// Contract:
//   - GET /plugin.json  → manifest (id/version/name/page_path/health_path)
//   - GET /healthz      → health check (meta-gateway probes this at install)
//   - GET /             → the page meta-gateway embeds in an iframe
//   - other paths       → plugin API, reverse-proxied by meta-gateway
//
// Every proxied request carries X-Plugin-Key (when configured at
// registration); this demo requires it unless -no-key is set.
//
// Build & run:
//
//	cd tools/plugins/demo-plugin
//	go build -o demo-plugin .
//	./demo-plugin -addr :9100 [-no-key]
//
// Then in the meta-gateway Store: "Register a plugin" →
// http://127.0.0.1:9100 (API key: demo-secret when not -no-key).
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"sync/atomic"
	"time"
)

const defaultKey = "demo-secret"

var hits atomic.Int64

type manifest struct {
	ID           string   `json:"id"`
	Version      string   `json:"version"`
	Name         string   `json:"name"`
	Description  string   `json:"description,omitempty"`
	Capabilities []string `json:"capabilities,omitempty"`
	PagePath     string   `json:"page_path,omitempty"`
	HealthPath   string   `json:"health_path,omitempty"`
}

func main() {
	addr := flag.String("addr", ":9100", "listen address")
	noKey := flag.Bool("no-key", false, "do not require X-Plugin-Key")
	keyFlag := flag.String("key", "", "expected X-Plugin-Key (default: $META_GATEWAY_PLUGIN_KEY, then demo-secret)")
	flag.Parse()

	// Resolve the expected key with host-first precedence: the gateway hands
	// managed plugins a random key via run_args {key} or the
	// META_GATEWAY_PLUGIN_KEY environment variable; plain sidecar runs keep
	// the historical demo-secret default.
	expectedKey := *keyFlag
	if expectedKey == "" {
		expectedKey = os.Getenv("META_GATEWAY_PLUGIN_KEY")
	}
	if expectedKey == "" {
		expectedKey = defaultKey
	}
	requireKey := !*noKey && expectedKey != ""
	mux := http.NewServeMux()

	mux.HandleFunc("/plugin.json", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(manifest{
			ID:           "demo-plugin",
			Version:      "1.0.0",
			Name:         "Demo Plugin",
			Description:  "Reference sidecar plugin: shows how the protocol works.",
			Capabilities: []string{"admin_page"},
			PagePath:     "/",
			HealthPath:   "/healthz",
		})
	})

	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		if requireKey && r.Header.Get("X-Plugin-Key") != expectedKey {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintln(w, "bad key")
			return
		}
		w.WriteHeader(http.StatusOK)
		fmt.Fprintln(w, "ok")
	})

	mux.HandleFunc("/api/stats", func(w http.ResponseWriter, r *http.Request) {
		if requireKey && r.Header.Get("X-Plugin-Key") != expectedKey {
			w.WriteHeader(http.StatusUnauthorized)
			fmt.Fprintln(w, "bad key")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"plugin":     "demo-plugin",
			"hits":       hits.Load(),
			"key_ok":     r.Header.Get("X-Plugin-Key") == expectedKey,
			"serverTime": time.Now().Format(time.RFC3339),
		})
	})

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprintf(w, `<!doctype html>
<html lang="zh">
<head>
<meta charset="utf-8">
<title>Demo Plugin</title>
<style>
  body { font-family: system-ui, sans-serif; margin: 0; padding: 28px 32px; background: #f7f4ef; color: #1c1a17; }
  h1 { font-size: 18px; margin: 0 0 6px; }
  .muted { color: #6f675e; font-size: 13px; }
  .card { background: #fffcf8; border: 1px solid #e6e0d6; border-radius: 6px; padding: 16px 18px; max-width: 560px; margin-top: 16px; }
  .row { display: flex; justify-content: space-between; padding: 6px 0; border-bottom: 1px dashed #e6e0d6; font-size: 13px; }
  .row:last-child { border-bottom: 0; }
  .row b { font-family: ui-monospace, monospace; }
  code { background: #efebe4; padding: 1px 5px; border-radius: 3px; font-size: 12px; }
</style>
</head>
<body>
<h1>Demo Plugin <span class="muted">v1.0.0</span></h1>
<p class="muted">这是一个 meta-gateway sidecar 插件协议参考实现。</p>
<div class="card">
  <div class="row"><span>插件 ID</span><b>demo-plugin</b></div>
  <div class="row"><span>服务时间</span><b id="time">-</b></div>
  <div class="row"><span>页面访问次数</span><b id="hits">-</b></div>
  <div class="row"><span>X-Plugin-Key 校验</span><b id="key">-</b></div>
</div>
<script>
fetch('/api/stats').then(r => r.json()).then(d => {
  document.getElementById('time').textContent = d.serverTime;
  document.getElementById('hits').textContent = d.hits;
  document.getElementById('key').textContent = d.key_ok ? '通过' : '未配置';
}).catch(() => {});
</script>
</body>
</html>`)
	})

	log.Printf("demo-plugin listening on %s (requireKey=%v)", *addr, requireKey)
	if err := http.ListenAndServe(*addr, mux); err != nil {
		log.Fatal(err)
	}
}
