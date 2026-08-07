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
			http.Error(w, `{"code":"token_required"}`, http.StatusUnauthorized)
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
