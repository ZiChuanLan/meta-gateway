package store

import (
	"fmt"
	"strings"
)

// MissingModel is a model a channel exposes (via its models_csv or the
// discovery snapshot) that no enabled route covers — traffic for it would
// 404/refuse because routing cannot resolve it.
type MissingModel struct {
	Model       string `json:"model"`
	ChannelID   int64  `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	Source      string `json:"source"` // "models_csv" | "discovered"
}

// MissingModels returns the set of channel-exposed models not covered by any
// enabled route (exact match or wildcard). Models.csv and the discovery
// snapshot are unioned per channel; a model is only reported once per
// channel. Empty channels/models.csv are ignored.
func (s *DB) MissingModels() ([]MissingModel, error) {
	channels, err := s.Channel.List()
	if err != nil {
		return nil, fmt.Errorf("missing models channels: %w", err)
	}
	discovered, err := s.DiscoveredModel.List(nil)
	if err != nil {
		return nil, fmt.Errorf("missing models discovery: %w", err)
	}
	routes, err := s.Route.ListEnabledPatterns()
	if err != nil {
		return nil, fmt.Errorf("missing models routes: %w", err)
	}

	covered := func(model string) bool {
		for _, pattern := range routes {
			if matchModelPattern(pattern, model) {
				return true
			}
		}
		return false
	}

	out := make([]MissingModel, 0, 16)
	seen := map[string]struct{}{}
	add := func(channelID int64, name, model, source string) {
		key := fmt.Sprintf("%d|%s", channelID, model)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		if strings.TrimSpace(model) == "" || covered(model) {
			return
		}
		out = append(out, MissingModel{Model: model, ChannelID: channelID, ChannelName: name, Source: source})
	}
	for _, channel := range channels {
		for _, model := range splitCSV(channel.ModelsCSV) {
			add(channel.ID, channel.Name, model, "models_csv")
		}
	}
	for _, model := range discovered {
		add(model.ChannelID, "", model.ModelName, "discovered")
	}
	return out, nil
}

func splitCSV(raw string) []string {
	var out []string
	for _, part := range strings.Split(raw, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
