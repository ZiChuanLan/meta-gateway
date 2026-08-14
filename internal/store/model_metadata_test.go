package store_test

import (
	"testing"

	"github.com/lan/meta-gateway/internal/domain"
)

func TestModelMetadataLifecycle(t *testing.T) {
	db := openTestDB(t)

	// Fresh library is empty.
	items, err := db.ModelMetadata.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 0 {
		t.Fatalf("fresh library = %d rows", len(items))
	}

	// Upsert creates.
	if err := db.ModelMetadata.Upsert(&domain.ModelMetadata{
		ModelName:        "deepseek-v4-flash",
		ContextWindow:    128000,
		InputModalities:  "text,image",
		OutputModalities: "text",
		SupportsThinking: 1,
		Vendor:           "DeepSeek",
		Notes:            "default",
	}); err != nil {
		t.Fatal(err)
	}
	got, err := db.ModelMetadata.Get("deepseek-v4-flash")
	if err != nil || got == nil {
		t.Fatalf("get after upsert: %v %v", got, err)
	}
	if got.ContextWindow != 128000 || got.SupportsThinking != 1 || got.Vendor != "DeepSeek" {
		t.Fatalf("upsert round trip = %+v", got)
	}

	// Upsert updates in place (same row).
	if err := db.ModelMetadata.Upsert(&domain.ModelMetadata{
		ModelName:       "deepseek-v4-flash",
		ContextWindow:   262144,
		SupportsThinking: 0,
	}); err != nil {
		t.Fatal(err)
	}
	got, _ = db.ModelMetadata.Get("deepseek-v4-flash")
	if got.ContextWindow != 262144 || got.SupportsThinking != 0 {
		t.Fatalf("update round trip = %+v", got)
	}
	items, _ = db.ModelMetadata.List()
	if len(items) != 1 {
		t.Fatalf("update created duplicate rows: %d", len(items))
	}

	// Delete removes; missing delete is not an error.
	if err := db.ModelMetadata.Delete("deepseek-v4-flash"); err != nil {
		t.Fatal(err)
	}
	got, _ = db.ModelMetadata.Get("deepseek-v4-flash")
	if got != nil {
		t.Fatalf("after delete = %+v", got)
	}
	if err := db.ModelMetadata.Delete("never-existed"); err != nil {
		t.Fatalf("delete missing: %v", err)
	}
}

func TestModelMetadataRequiresName(t *testing.T) {
	db := openTestDB(t)
	if err := db.ModelMetadata.Upsert(&domain.ModelMetadata{ContextWindow: 100}); err == nil {
		t.Fatal("upsert without model_name must fail")
	}
}
