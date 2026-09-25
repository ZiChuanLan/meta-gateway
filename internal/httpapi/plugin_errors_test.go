package httpapi

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"

	"github.com/lan/meta-gateway/internal/plugins"
)

// The response a failed plugin operation produces. Before this mapping, every
// unrecognized failure — a registry timeout, a 403 from the artifact host, a
// checksum mismatch — answered a bare "internal_error" and logged nothing, so
// installing a plugin could fail with no way to tell why (2026-09-25).
func TestWritePluginErrorMapsCodesAndStatus(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		wantStatus int
		wantCode   string
	}{
		{
			name:       "not found keeps its own code",
			err:        plugins.ErrNotFound,
			wantStatus: 404,
			wantCode:   "plugin_not_found",
		},
		{
			name:       "already installed is a conflict",
			err:        plugins.ErrAlreadyExists,
			wantStatus: 409,
			wantCode:   "plugin_already_installed",
		},
		{
			name: "unreachable registry is reported as such",
			// What InstallMarketFrom returns when no source answered.
			err:        fmt.Errorf("plugin_market_unavailable"),
			wantStatus: 502,
			wantCode:   "plugin_market_unavailable",
		},
		{
			name: "release lookup failure keeps the upstream code",
			// The wrapping the service adds around a GitHub API failure.
			err:        fmt.Errorf("plugin_release_fetch: %w", fmt.Errorf("context deadline exceeded")),
			wantStatus: 502,
			wantCode:   "plugin_release_fetch",
		},
		{
			name:       "artifact download failure keeps the upstream code",
			err:        fmt.Errorf("plugin_artifact_download: dial tcp: connection refused"),
			wantStatus: 502,
			wantCode:   "plugin_artifact_download",
		},
		{
			name:       "checksum mismatch stays readable",
			err:        fmt.Errorf("plugin_artifact_checksum_mismatch"),
			wantStatus: 502,
			wantCode:   "plugin_artifact_checksum_mismatch",
		},
		{
			name:       "local install failure names its step",
			err:        fmt.Errorf("plugin_stage_create: permission denied"),
			wantStatus: 500,
			wantCode:   "plugin_stage_create",
		},
		{
			name:       "an error without a code still answers a stable one",
			err:        fmt.Errorf("something exploded"),
			wantStatus: 500,
			wantCode:   "internal_error",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			writePluginError(rec, tc.err)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d (body %s)", rec.Code, tc.wantStatus, rec.Body.String())
			}
			var body struct {
				Error string `json:"error"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body %q: %v", rec.Body.String(), err)
			}
			if body.Error != tc.wantCode {
				t.Fatalf("error = %q, want %q", body.Error, tc.wantCode)
			}
		})
	}
}
