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

func TestAdminModelDiscardHTTP(t *testing.T) {
	db, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	channel, err := db.Channel.Create(&domain.Channel{Name: "upstream", Status: domain.StatusEnabled, ModelSyncMode: domain.ModelSyncModeManual, Weight: 100})
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
	route, err := db.Route.Create(&domain.Route{ModelPattern: "public", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	member, err := db.RouteMember.Create(&domain.RouteMember{RouteID: route, ChannelID: channel, MappingJSON: `{"real":"old"}`, Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	sync("new")
	h := &AdminHandler{db: db}
	router := chi.NewRouter()
	router.Route("/admin", h.Register)
	call := func(path string, body any) *httptest.ResponseRecorder {
		t.Helper()
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		request := httptest.NewRequest(http.MethodPost, "/admin/models/changes"+path, bytes.NewReader(raw))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		return response
	}
	list := func() store.ModelChanges {
		t.Helper()
		request := httptest.NewRequest(http.MethodGet, "/admin/models/changes", nil)
		response := httptest.NewRecorder()
		router.ServeHTTP(response, request)
		if response.Code != 200 {
			t.Fatalf("list: %d %s", response.Code, response.Body)
		}
		var changes store.ModelChanges
		if err := json.Unmarshal(response.Body.Bytes(), &changes); err != nil {
			t.Fatal(err)
		}
		return changes
	}
	var removed store.ModelChange
	for _, x := range list().Items {
		if x.Kind == "removed" {
			removed = x
		}
	}
	if len(removed.Members) != 1 || !removed.Members[0].Deletable {
		t.Fatalf("removal: %+v", removed)
	}
	if response := call("/discard-apply", store.ModelChangeDiscardRequest{ChangeIDs: []int64{removed.ID}, MemberIDs: []int64{member}}); response.Code != 409 {
		t.Fatalf("tokenless apply: %d", response.Code)
	}
	response := call("/discard-preview", store.ModelChangeDiscardRequest{ChangeIDs: []int64{removed.ID}, MemberIDs: []int64{member}})
	if response.Code != 200 {
		t.Fatalf("preview: %d %s", response.Code, response.Body)
	}
	var preview store.ModelChangeDiscardPreview
	if err := json.Unmarshal(response.Body.Bytes(), &preview); err != nil {
		t.Fatal(err)
	}
	if len(preview.Items) != 1 || preview.Routes != 1 || !preview.Items[0].RouteDeleted {
		t.Fatalf("preview: %+v", preview)
	}
	response = call("/discard-apply", store.ModelChangeDiscardRequest{ChangeIDs: []int64{removed.ID}, MemberIDs: []int64{member}, PreviewToken: preview.PreviewToken})
	if response.Code != 200 {
		t.Fatalf("apply: %d %s", response.Code, response.Body)
	}
	var result store.ModelChangeDiscardResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.Removed != 1 || result.Routes != 1 {
		t.Fatalf("result: %+v", result)
	}
	if route, err := db.Route.GetByID(route); err != nil || route != nil {
		t.Fatalf("route kept: %+v %v", route, err)
	}
	for _, x := range list().Items {
		if x.ID == removed.ID && x.Status != "applied" {
			t.Fatalf("status: %+v", x)
		}
	}
	if response := call("/discard-preview", map[string]any{}); response.Code != 400 {
		t.Fatalf("invalid request: %d", response.Code)
	}
}
