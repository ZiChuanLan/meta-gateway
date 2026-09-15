package adapters

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestCoercePlatformUserID covers the New-API user-id shapes seen in the wild:
// plain JSON numbers, AAH exports that quote the value, and the invalid forms
// the admin API must reject instead of silently writing a zero id.
func TestCoercePlatformUserID(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want int64
		ok   bool
	}{
		{name: "number", raw: `1544`, want: 1544, ok: true},
		{name: "json.Number", raw: `1544`, want: 1544, ok: true},
		{name: "quoted", raw: `"1544"`, want: 1544, ok: true},
		{name: "quoted with spaces", raw: `" 1544 "`, want: 1544, ok: true},
		{name: "zero", raw: `0`, ok: false},
		{name: "negative", raw: `-3`, ok: false},
		{name: "quoted zero", raw: `"0"`, ok: false},
		{name: "non-numeric", raw: `"abc"`, ok: false},
		{name: "float", raw: `1.5`, ok: false},
		{name: "null", raw: `null`, ok: false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			decoder := json.NewDecoder(strings.NewReader(tc.raw))
			decoder.UseNumber()
			var value any
			if err := decoder.Decode(&value); err != nil {
				t.Fatalf("decode %s: %v", tc.raw, err)
			}
			got, ok := CoercePlatformUserID(value)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("CoercePlatformUserID(%s) = (%d, %v), want (%d, %v)", tc.raw, got, ok, tc.want, tc.ok)
			}
		})
	}
}
