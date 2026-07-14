package main

import (
	"encoding/json"
	"io"
	"net/http"
	"os"
	"strings"
)

const secret = "e2e-upstream-secret"

func main() {
	if len(os.Args) == 2 && os.Args[1] == "healthcheck" {
		response, err := http.Get("http://127.0.0.1:8080/healthz")
		if err != nil || response.StatusCode != http.StatusOK {
			os.Exit(1)
		}
		_ = response.Body.Close()
		return
	}
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+secret {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/v1/models"):
			writeJSON(w, map[string]any{"data": []map[string]string{{"id": "e2e-model"}}})
		case strings.HasSuffix(r.URL.Path, "/api/user/checkin"):
			writeJSON(w, map[string]any{"success": true, "data": map[string]int{"reward": 7}})
		case strings.Contains(r.URL.Path, "/fail/v1/chat/completions"):
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = io.WriteString(w, `{"error":"retry"}`)
		case strings.HasSuffix(r.URL.Path, "/v1/chat/completions"):
			body, _ := io.ReadAll(r.Body)
			if strings.Contains(string(body), `"stream":true`) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n")
				return
			}
			writeJSON(w, map[string]any{"id": "e2e-chat", "object": "chat.completion", "model": "e2e-model", "choices": []map[string]any{{"message": map[string]string{"role": "assistant", "content": "ok"}}}})
		default:
			http.NotFound(w, r)
		}
	})
	if err := http.ListenAndServe(":8080", handler); err != nil {
		panic(err)
	}
}

func writeJSON(w http.ResponseWriter, value any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
