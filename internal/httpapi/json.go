package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
)

const defaultAdminBodyBytes int64 = 2 << 20

type adminBodyLimitKey struct{}

func withAdminBodyLimit(maxBytes int64) func(http.Handler) http.Handler {
	if maxBytes <= 0 {
		maxBytes = defaultAdminBodyBytes
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), adminBodyLimitKey{}, maxBytes)))
		})
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any, maxBytes int64, strict bool) error {
	if maxBytes <= 0 {
		if configured, ok := r.Context().Value(adminBodyLimitKey{}).(int64); ok {
			maxBytes = configured
		} else {
			maxBytes = defaultAdminBodyBytes
		}
	}
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBytes))
	if strict {
		decoder.DisallowUnknownFields()
	}
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}
