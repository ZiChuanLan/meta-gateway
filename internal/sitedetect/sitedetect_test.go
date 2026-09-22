package sitedetect

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestDetectByTitle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<html><head><title>WONG 公益 API - New API 中转站</title></head></html>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	result, err := Detect(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if result.Family != "new-api" || !result.TitleMatched {
		t.Fatalf("result=%+v", result)
	}
}

func TestDetectOneAPITitle(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<title>My One API Dashboard</title>`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	result, err := Detect(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if result.Family != "one-api" || result.SiteType != "ONE_API" {
		t.Fatalf("result=%+v", result)
	}
}

func TestDetectSub2APIByEndpointShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<html><head><title>Generic Dashboard</title></head></html>`))
		case "/api/v1/auth/me":
			// The real sub2api body: `response.Error(401, …)` writes an INTEGER code
			// (sub2api backend/internal/pkg/response/response.go), which the previous
			// `Code string` decode silently failed to match.
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"code":401,"message":"User not authenticated"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	result, err := Detect(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if result.Family != "sub2api" {
		t.Fatalf("result=%+v", result)
	}
}

func TestDetectWhiteLabelViaCompatHeader(t *testing.T) {
	cases := []struct {
		name     string
		message  string
		wantSite string
		wantFam  string
	}{
		{"v-api", `{"success":false,"message":"Invalid X-Api-User header"}`, "V_API", "new-api"},
		{"rix", `{"success":false,"message":"Rix-Api-User required"}`, "RIX_API", "new-api"},
		{"new-api", `{"success":false,"message":"Unauthorized: New-API-User mismatch"}`, "NEW_API", "new-api"},
		{"one-api", `{"success":false,"message":"One-API-User not provided"}`, "ONE_API", "one-api"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/":
					_, _ = w.Write([]byte(`<html><head><title>Untitled</title></head></html>`))
				case "/api/user/self":
					http.Error(w, tc.message, http.StatusUnauthorized)
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)

			result, err := Detect(context.Background(), server.Client(), server.URL)
			if err != nil {
				t.Fatal(err)
			}
			if result.SiteType != tc.wantSite || result.Family != tc.wantFam {
				t.Fatalf("result=%+v want %s/%s", result, tc.wantSite, tc.wantFam)
			}
			if result.Evidence != "compat-header" {
				t.Fatalf("evidence=%q want compat-header", result.Evidence)
			}
		})
	}
}

func TestDetectNewAPIByAuthError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<html><head><title>Untitled</title></head></html>`))
		case "/api/user/self":
			http.Error(w, `{"success":false,"message":"Unauthorized, invalid access token"}`, http.StatusUnauthorized)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	result, err := Detect(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if result.Family != "new-api" {
		t.Fatalf("result=%+v", result)
	}
}

func TestDetectUnknownSite(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)

	result, err := Detect(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if result.Family != "" {
		t.Fatalf("expected no match, got %+v", result)
	}
}

// A provider that is NOT a New-API host must not be labelled one just because it
// answers 401 on /api/user/self. Both bodies below are real responses captured
// from the live services; before this was tightened, Zhipu was reported as
// `new-api` and the connection dialog then overwrote the operator's explicit
// provider choice (and its correct preset base URL) on blur.
func TestDetectDoesNotClaimNewAPIForUnrelatedProviders(t *testing.T) {
	cases := []struct {
		name         string
		selfBody     string
		selfStatus   int
		authMe       string
		authMeStatus int
	}{
		{
			name:       "zhipu error envelope",
			selfBody:   `{"error":{"code":"1001","message":"Header中未收到Authorization参数，无法进行身份验证。"}}`,
			selfStatus: http.StatusUnauthorized,
		},
		{
			name:         "deepseek plain text (also must not read as sub2api)",
			selfBody:     `Authentication Fails (governor)`,
			authMe:       `Authentication Fails (governor)`,
			authMeStatus: http.StatusUnauthorized,
		},
		{
			// Measured: Moonshot answers this unrelated path with its own 404
			// envelope `{"code":5,…,"message":"没找到对象",…}`, which has the same
			// SHAPE as sub2api's error envelope. Requiring 401 excludes it.
			name:         "moonshot 404 envelope with a numeric code",
			selfBody:     `{"code":5,"error":"url.not_found","message":"没找到对象"}`,
			selfStatus:   http.StatusNotFound,
			authMe:       `{"code":5,"error":"url.not_found","message":"没找到对象"}`,
			authMeStatus: http.StatusNotFound,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/":
					_, _ = w.Write([]byte(`<html><head><title>Generic Provider</title></head></html>`))
				case "/api/user/self":
					status := tc.selfStatus
					if status == 0 {
						status = http.StatusUnauthorized
					}
					w.WriteHeader(status)
					_, _ = w.Write([]byte(tc.selfBody))
				case "/api/v1/auth/me":
					if tc.authMe == "" {
						http.NotFound(w, r)
						return
					}
					status := tc.authMeStatus
					if status == 0 {
						status = http.StatusUnauthorized
					}
					w.WriteHeader(status)
					_, _ = w.Write([]byte(tc.authMe))
				default:
					http.NotFound(w, r)
				}
			}))
			t.Cleanup(server.Close)

			result, err := Detect(context.Background(), server.Client(), server.URL)
			if err != nil {
				t.Fatal(err)
			}
			if result.Family != "" {
				t.Fatalf("family = %q (evidence %q), want no claim", result.Family, result.Evidence)
			}
		})
	}
}

// A real New-API host still resolves, via its own JSON envelope.
func TestDetectNewAPIByEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/":
			_, _ = w.Write([]byte(`<html><head><title>Generic Dashboard</title></head></html>`))
		case "/api/user/self":
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"success":false,"message":"无权进行此操作，未登录且未提供 access token"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)

	result, err := Detect(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if result.Family != "new-api" || result.SiteType != "NEW_API" {
		t.Fatalf("result=%+v", result)
	}
}
