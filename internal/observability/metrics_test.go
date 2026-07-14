package observability

import (
	"bytes"
	"strings"
	"testing"
	"time"
)

func TestRegistryUsesBoundedHTTPLabels(t *testing.T) {
	registry := NewRegistry()
	registry.ObserveHTTP("GET", "/admin/sites/{id}", 200, 25*time.Millisecond)
	registry.RateLimited("admin")
	var output bytes.Buffer
	if err := registry.WritePrometheus(&output, true); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	for _, expected := range []string{`meta_gateway_ready 1`, `method="GET"`, `route="/admin/sites/{id}"`, `status_class="2xx"`, `scope="admin"`} {
		if !strings.Contains(text, expected) {
			t.Fatalf("missing %q in %s", expected, text)
		}
	}
}
