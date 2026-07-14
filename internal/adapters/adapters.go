// Package adapters contains stateless upstream platform integrations.
package adapters

import (
	"context"
	"net/http"
	"strings"
)

type ModelAdapter interface {
	Name() string
	ListModels(ctx context.Context, baseURL, apiKey string) ([]string, error)
}

type Registry struct {
	adapters        map[string]ModelAdapter
	checkinAdapters map[string]CheckinAdapter
}

func NewRegistry(client *http.Client) *Registry {
	openAI := NewOpenAIModelAdapter("openai-compatible", client)
	newAPI := NewOpenAIModelAdapter("new-api", client)
	newAPICheckin := NewJSONCheckinAdapter("new-api", client, true)
	oneAPICheckin := NewJSONCheckinAdapter("one-api", client, false)
	return &Registry{adapters: map[string]ModelAdapter{
		"openai":            openAI,
		"openai-compatible": openAI,
		"openaicompat":      openAI,
		"new-api":           newAPI,
		"newapi":            newAPI,
	}, checkinAdapters: map[string]CheckinAdapter{
		"new-api": newAPICheckin,
		"newapi":  newAPICheckin,
		"one-api": oneAPICheckin,
		"oneapi":  oneAPICheckin,
	}}
}

func (r *Registry) ResolveCheckin(platform string) (CheckinAdapter, bool) {
	adapter, ok := r.checkinAdapters[canonical(platform)]
	return adapter, ok
}

// Resolve gives channel type_hint precedence over the site's platform.
func (r *Registry) Resolve(typeHint, platform string) (ModelAdapter, bool) {
	name := canonical(typeHint)
	if name == "" {
		name = canonical(platform)
	}
	adapter, ok := r.adapters[name]
	return adapter, ok
}

func canonical(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}
