package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/lan/meta-gateway/internal/domain"
	"github.com/lan/meta-gateway/internal/store"
)

func TestAdminModelChangesHTTP(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	channel, err := db.Channel.Create(&domain.Channel{Name: "upstream", Status: domain.StatusEnabled, ModelSyncMode: domain.ModelSyncModeAuto, Weight: 100})
	if err != nil {
		t.Fatal(err)
	}
	sync := func(models ...string) {
		t.Helper()
		if _, err := db.DiscoveredModel.Reconcile(t.Context(), store.ReconcileInput{ChannelID: channel, Models: models, CheckedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	sync("old")
	sync("new")
	h := &AdminHandler{db: db}
	router := chi.NewRouter()
	router.Route("/admin", h.Register)
	call := func(method, path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(method, "/admin/models/changes"+path, bytes.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	response := call(http.MethodGet, "", nil)
	if response.Code != 200 {
		t.Fatalf("list: %d %s", response.Code, response.Body)
	}
	var changes store.ModelChanges
	if err := json.Unmarshal(response.Body.Bytes(), &changes); err != nil {
		t.Fatal(err)
	}
	var removed store.ModelChange
	for _, x := range changes.Items {
		if x.Kind == "removed" {
			removed = x
		}
	}
	if len(removed.Members) != 1 || changes.Summary.Removed != 1 {
		t.Fatalf("response: %+v", changes)
	}
	req := store.ModelChangeRequest{ChangeIDs: []int64{removed.ID}, MemberIDs: []int64{removed.Members[0].MemberID}, TargetChannelID: channel, TargetModel: "new"}
	response = call(http.MethodPost, "/preview", req)
	if response.Code != 200 {
		t.Fatalf("preview: %d %s", response.Code, response.Body)
	}
	var preview store.ModelChangePreview
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	req.PreviewToken = preview.PreviewToken
	sync("new")
	response = call(http.MethodPost, "/apply", req)
	if response.Code != 409 {
		t.Fatalf("stale: %d %s", response.Code, response.Body)
	}
	req.TargetModel = "missing"
	response = call(http.MethodPost, "/preview", req)
	if response.Code != 409 {
		t.Fatalf("target: %d", response.Code)
	}
	req.TargetModel = "new"
	response = call(http.MethodPost, "/preview", req)
	if response.Code != 200 {
		t.Fatalf("preview: %s", response.Body)
	}
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	req.PreviewToken = preview.PreviewToken
	response = call(http.MethodPost, "/apply", req)
	if response.Code != 200 {
		t.Fatalf("apply: %d %s", response.Code, response.Body)
	}
	response = call(http.MethodPost, "/ignore", map[string]any{"ids": []int64{removed.ID}})
	if response.Code != 409 {
		t.Fatalf("ignore applied: %d", response.Code)
	}
	response = call(http.MethodPost, "/preview", map[string]any{})
	if response.Code != 400 {
		t.Fatalf("invalid request: %d", response.Code)
	}
	response = call(http.MethodGet, "", nil)
	if err := json.Unmarshal(response.Body.Bytes(), &changes); err != nil {
		t.Fatal(err)
	}
	for _, x := range changes.Items {
		if x.ID == removed.ID && x.Status != "applied" {
			t.Fatalf("status: %+v", x)
		}
	}
}
