package httpapi_test

import (
	"bytes"
	"io"
	"math"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

func TestImageEditShimKeepsRouteGroupAndMemberBilling(t *testing.T) {
	upstream := newImageUpstream(t, &imageUpstream{})
	defer upstream.Close()
	serverURL, token, routeID, db := setupImageRelayWithStore(t, upstream.URL, "grok-imagine-image-edit")
	var channelID, keyID int64
	if err := db.QueryRow(`SELECT channel_id FROM route_members WHERE route_id = ?`, routeID).Scan(&channelID); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow(`SELECT id FROM downstream_keys LIMIT 1`).Scan(&keyID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.RouteMember.Create(&domain.RouteMember{
		RouteID: routeID, ChannelID: channelID, GroupName: "private-images",
		Enabled: true, Priority: 1, Weight: 100, PricePromptPer1k: 2,
	}); err != nil {
		t.Fatal(err)
	}
	key, err := db.DownstreamKey.GetByID(keyID)
	if err != nil {
		t.Fatal(err)
	}
	key.RouteGroupName = "private-images"
	if err := db.DownstreamKey.Update(key); err != nil {
		t.Fatal(err)
	}
	put(t, serverURL+"/admin/routes/"+strconv.FormatInt(routeID, 10), map[string]any{
		"model_pattern": "grok-imagine-image-edit", "enabled": true, "image_edit_shim": true,
	})
	status, body, _ := postChat(t, serverURL, token, chatWithImage("make it purple"), false)
	if status != http.StatusOK {
		t.Fatalf("status=%d body=%s", status, body)
	}
	var prompt, completion, total int
	var cost float64
	if err := db.QueryRow(`SELECT prompt_tokens, completion_tokens, total_tokens, cost FROM usage_records WHERE downstream_key_id = ?`, keyID).Scan(&prompt, &completion, &total, &cost); err != nil {
		t.Fatal(err)
	}
	if prompt != 10 || completion != 0 || total != 10 || math.Abs(cost-0.02) > 1e-9 {
		t.Fatalf("image billing: prompt=%d completion=%d total=%d cost=%v", prompt, completion, total, cost)
	}
}

func TestImageEditsMultipartStreamPreservesUploadAndUsage(t *testing.T) {
	sent := make(chan []byte, 1)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sent <- body
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: image_edit.completed\ndata: {\"b64_json\":\"QUFBQQ==\",\"usage\":{\"input_tokens\":7,\"output_tokens\":3,\"total_tokens\":10}}\n\n")
	}))
	defer upstream.Close()
	serverURL, token, _, db := setupImageRelayWithStore(t, upstream.URL, "gpt-image-2")
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for _, field := range [][2]string{{"model", "gpt-image-2"}, {"prompt", "add a hat"}, {"stream", "true"}, {"partial_images", "2"}} {
		if err := writer.WriteField(field[0], field[1]); err != nil {
			t.Fatal(err)
		}
	}
	file, err := writer.CreateFormFile("image", "input.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("original image bytes")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, serverURL+"/v1/images/edits", bytes.NewReader(body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK || !strings.HasPrefix(response.Header.Get("Content-Type"), "text/event-stream") {
		t.Fatalf("status=%d content-type=%s", response.StatusCode, response.Header.Get("Content-Type"))
	}
	if !bytes.Equal(<-sent, body.Bytes()) {
		t.Fatal("multipart upload changed during forwarding")
	}
	var total int
	if err := db.QueryRow(`SELECT total_tokens FROM usage_records LIMIT 1`).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 10 {
		t.Fatalf("stream usage lost: total=%d", total)
	}
}

func TestImageEditAliasPreservesFilesAndOptionalFields(t *testing.T) {
	captured := &imageUpstream{}
	upstream := newImageUpstream(t, captured)
	defer upstream.Close()
	serverURL, token, routeID := setupImageRelay(t, upstream.URL, "image-alias")
	put(t, serverURL+"/admin/routes/"+strconv.FormatInt(routeID, 10), map[string]any{
		"model_pattern": "image-alias", "enabled": true, "mapping_json": `{"real":"gpt-image-2"}`,
	})
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	fields := [][2]string{{"model", "image-alias"}, {"prompt", "add a hat"}, {"stream", "false"}, {"partial_images", "0"}, {"background", "transparent"}}
	for _, field := range fields {
		if err := writer.WriteField(field[0], field[1]); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"image", "mask"} {
		file, err := writer.CreateFormFile(name, name+".png")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write([]byte(name + " original bytes")); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request, err := http.NewRequest(http.MethodPost, serverURL+"/v1/images/edits", bytes.NewReader(body.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if _, err := io.Copy(io.Discard, response.Body); err != nil {
		t.Fatal(err)
	}
	if response.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", response.StatusCode)
	}
	replayed := httptest.NewRequest(http.MethodPost, "/v1/images/edits", bytes.NewReader(captured.body))
	replayed.Header.Set("Content-Type", captured.contentType)
	if err := replayed.ParseMultipartForm(1 << 20); err != nil {
		t.Fatal(err)
	}
	defer replayed.MultipartForm.RemoveAll()
	fields[0][1] = "gpt-image-2"
	for _, field := range fields {
		if got := replayed.FormValue(field[0]); got != field[1] {
			t.Fatalf("%s=%q want=%q", field[0], got, field[1])
		}
	}
	for _, name := range []string{"image", "mask"} {
		file, _, err := replayed.FormFile(name)
		if err != nil {
			t.Fatal(err)
		}
		value, readErr := io.ReadAll(file)
		_ = file.Close()
		if readErr != nil || string(value) != name+" original bytes" {
			t.Fatalf("%s upload changed: %q, %v", name, value, readErr)
		}
	}
}
