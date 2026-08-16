package httpapi

import (
	"net/http"
	"regexp"
	"strings"

	"github.com/lan/meta-gateway/internal/alerts"
	"github.com/lan/meta-gateway/internal/store"
)

// listErrorRules returns all error passthrough rules.
func (h *AdminHandler) listErrorRules(w http.ResponseWriter, r *http.Request) {
	items, err := h.db.ErrorRule.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if items == nil {
		items = []store.ErrorPassRule{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

// listAlertRules returns all rules plus the metric catalog for the UI.
func (h *AdminHandler) listAlertRules(w http.ResponseWriter, r *http.Request) {
	items, err := h.db.AlertRule.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if items == nil {
		items = []store.AlertRule{}
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"items":   items,
		"metrics": alerts.MetricDescriptions,
	})
}

func (h *AdminHandler) decodeAlertRule(w http.ResponseWriter, r *http.Request) (*store.AlertRule, bool) {
	var rule store.AlertRule
	if err := decodeJSON(w, r, &rule, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return nil, false
	}
	if err := alerts.ValidateRule(&rule); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return nil, false
	}
	return &rule, true
}

func (h *AdminHandler) createAlertRule(w http.ResponseWriter, r *http.Request) {
	rule, ok := h.decodeAlertRule(w, r)
	if !ok {
		return
	}
	if err := h.db.AlertRule.Upsert(rule); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (h *AdminHandler) updateAlertRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	rule, ok := h.decodeAlertRule(w, r)
	if !ok {
		return
	}
	rule.ID = id
	if err := h.db.AlertRule.Upsert(rule); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (h *AdminHandler) deleteAlertRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.AlertRule.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// listPromptGuards returns all guard rules.
func (h *AdminHandler) listPromptGuards(w http.ResponseWriter, r *http.Request) {
	items, err := h.db.PromptGuard.List()
	if err != nil {
		writeStoreError(w, err)
		return
	}
	if items == nil {
		items = []store.PromptGuardRule{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (h *AdminHandler) decodePromptGuard(w http.ResponseWriter, r *http.Request) (*store.PromptGuardRule, bool) {
	var rule store.PromptGuardRule
	if err := decodeJSON(w, r, &rule, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return nil, false
	}
	rule.Name = strings.TrimSpace(rule.Name)
	rule.Pattern = strings.TrimSpace(rule.Pattern)
	if rule.Name == "" || rule.Pattern == "" {
		writeError(w, http.StatusBadRequest, "name and pattern are required")
		return nil, false
	}
	if _, err := regexp.Compile(rule.Pattern); err != nil {
		writeError(w, http.StatusBadRequest, "invalid regex: "+err.Error())
		return nil, false
	}
	switch rule.Action {
	case "mask", "reject", "exclude":
	default:
		writeError(w, http.StatusBadRequest, "action must be mask, reject or exclude")
		return nil, false
	}
	if rule.Action == "exclude" && strings.TrimSpace(rule.ExcludeChannels) == "" {
		writeError(w, http.StatusBadRequest, "exclude_channels required for exclude action")
		return nil, false
	}
	return &rule, true
}

func (h *AdminHandler) createPromptGuard(w http.ResponseWriter, r *http.Request) {
	rule, ok := h.decodePromptGuard(w, r)
	if !ok {
		return
	}
	if err := h.db.PromptGuard.Upsert(rule); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (h *AdminHandler) updatePromptGuard(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	rule, ok := h.decodePromptGuard(w, r)
	if !ok {
		return
	}
	rule.ID = id
	if err := h.db.PromptGuard.Upsert(rule); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

func (h *AdminHandler) deletePromptGuard(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.PromptGuard.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// validateErrorRule normalizes and checks one rule payload; returns the
// normalized rule or a 400 error response.
func (h *AdminHandler) validateErrorRule(w http.ResponseWriter, r *store.ErrorPassRule) bool {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return false
	}
	if r.StatusCode < 0 || r.StatusCode > 599 {
		writeError(w, http.StatusBadRequest, "status_code must be 0-599")
		return false
	}
	switch r.Action {
	case store.ErrorRulePassthrough, store.ErrorRuleRewrite, store.ErrorRuleIgnoreMonitor:
	default:
		writeError(w, http.StatusBadRequest, "action must be passthrough, rewrite or ignore_monitor")
		return false
	}
	if r.Action == store.ErrorRuleRewrite && (r.RewriteTo < 100 || r.RewriteTo > 599) {
		writeError(w, http.StatusBadRequest, "rewrite_to must be 100-599")
		return false
	}
	if r.ChannelID < 0 {
		writeError(w, http.StatusBadRequest, "channel_id must be >= 0")
		return false
	}
	return true
}

// createErrorRule inserts a new rule.
func (h *AdminHandler) createErrorRule(w http.ResponseWriter, r *http.Request) {
	var rule store.ErrorPassRule
	if err := decodeJSON(w, r, &rule, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !h.validateErrorRule(w, &rule) {
		return
	}
	id, err := h.db.ErrorRule.Create(&rule)
	if err != nil {
		writeStoreError(w, err)
		return
	}
	rule.ID = id
	writeJSON(w, http.StatusOK, rule)
}

// updateErrorRule replaces one rule (hot reload: next request reads it live).
func (h *AdminHandler) updateErrorRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	var rule store.ErrorPassRule
	if err := decodeJSON(w, r, &rule, 0, false); err != nil {
		writeError(w, http.StatusBadRequest, "invalid body")
		return
	}
	if !h.validateErrorRule(w, &rule) {
		return
	}
	rule.ID = id
	if err := h.db.ErrorRule.Update(&rule); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

// deleteErrorRule removes one rule.
func (h *AdminHandler) deleteErrorRule(w http.ResponseWriter, r *http.Request) {
	id, ok := pathID(w, r, "id")
	if !ok {
		return
	}
	if err := h.db.ErrorRule.Delete(id); err != nil {
		writeStoreError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}
