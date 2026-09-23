package httpapi

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/lan/meta-gateway/internal/modelcatalog"
)

// modelBootstrapQueueSize bounds how many newly routed models can be waiting
// for their one-off catalog sync. It only has to absorb a burst of creates: a
// dropped entry is not lost work, it is deferred to the next periodic sweep
// (which covers every enabled route pattern anyway).
const modelBootstrapQueueSize = 32

// modelBootstrapTimeout bounds one model's catalog fetch.
const modelBootstrapTimeout = 2 * time.Minute

// bootstrapNewModel fills in what the gateway can know about a model that was
// just routed, so the operator does not have to ask for it:
//
//   - the built-in classifier runs inline — it is local, idempotent, and skips
//     any row an operator or a curated catalog already owns;
//   - the external indexes are consulted for that one model in the background,
//     because it is a network round trip that must not sit inside the save.
//
// A wildcard pattern is skipped: it is a matcher, not a callable model id, and
// no catalog lists "gpt-*".
func (h *AdminHandler) bootstrapNewModel(pattern string) {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" || strings.ContainsAny(pattern, "*?") {
		return
	}
	if err := h.db.ModelCapability.AutoTag(pattern); err != nil && h.logger != nil {
		h.logger.Warn("model auto-tag failed", "category", "catalog", "model", pattern, "error", err)
	}
	h.queueModelBootstrap(pattern)
}

// queueModelBootstrap hands a model to the background bootstrap worker. It
// never blocks the request: with no worker wired (a deployment without catalog
// sources) the send is dropped, and a full queue defers to the periodic sweep
// rather than holding the HTTP handler until a download finishes.
func (h *AdminHandler) queueModelBootstrap(pattern string) {
	queue := h.modelBootstraps
	if queue == nil {
		return
	}
	select {
	case queue <- pattern:
	default:
		if h.logger != nil {
			h.logger.Warn("model bootstrap queue full, deferring to the next sweep",
				"category", "catalog", "model", pattern)
		}
	}
}

// ModelBootstrapQueue exposes the queue the background worker drains. It is nil
// when no catalog service is wired, which is also when the worker must not be
// started.
func (h *AdminHandler) ModelBootstrapQueue() <-chan string {
	return h.modelBootstraps
}

// runModelBootstraps drains newly routed models and syncs each one on its own,
// so a save is followed by a filled-in registry row within seconds instead of
// whenever the periodic sweep next fires. A failed sync is logged and dropped:
// the sweep is the safety net, and one bad model must never stall the queue.
func runModelBootstraps(ctx context.Context, logger *slog.Logger, queue <-chan string, runner modelcatalog.Runner, prices bool) {
	policy := modelcatalog.DefaultPolicy
	policy.Prices = prices
	for {
		select {
		case <-ctx.Done():
			return
		case pattern := <-queue:
			syncCtx, cancel := context.WithTimeout(ctx, modelBootstrapTimeout)
			state, err := runner.Sync(syncCtx, []string{pattern}, policy)
			cancel()
			if err != nil {
				logger.Warn("model bootstrap sync failed", "category", "catalog",
					"model", pattern, "error", err)
				continue
			}
			logger.Info("model bootstrap sync done", "category", "catalog",
				"model", pattern, "capabilities", state.Capabilities,
				"metadata", state.Metadata, "prices", state.Prices)
		}
	}
}
