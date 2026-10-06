package httpapi

import (
	"log"

	"github.com/lan/meta-gateway/internal/livetrace"
	"github.com/lan/meta-gateway/internal/proxy"
	"github.com/lan/meta-gateway/internal/relay"
	"github.com/lan/meta-gateway/internal/usage"
)

// attemptAccountant books one finished relay attempt and returns it as the
// callback `writeUpstreamResult` invokes once the body has reached the client.
//
// Every relay entry point must do the same three things at that moment:
//
//   - persist usage, which resolves the member's OWN prices by id — the
//     (route, channel) pair is not unique across route groups — so the member
//     id from the attempt metadata has to be copied onto the request first;
//   - backfill the proxy log with the timing and client family that are only
//     known after the copy;
//   - close the live-trace entry with the byte counts.
//
// The registered /v1 endpoints and the unregistered custom-path passthrough held
// private copies of this block, and they drifted: the custom path went without
// the hook-origin context and the hook-decision response header. Sharing the code
// makes "the same accounting on every entry" structural instead of something a
// reviewer has to spot.
func (h *RelayHandler) attemptAccountant(
	req proxy.Request,
	meta *proxy.AttemptMeta,
	requestID, clientFamily string,
) func(tokens usage.Tokens, status int, firstByteMs int, bytesSent int64) {
	return func(tokens usage.Tokens, status int, firstByteMs int, bytesSent int64) {
		channelID := int64(0)
		if meta != nil {
			channelID = meta.ChannelID
			req.RouteID = meta.RouteID
			req.MemberID = meta.MemberID
			req.UpstreamModel = meta.UpstreamModel
			req.GrayAttempt = meta.GrayAttempt
		}
		h.proxy.RecordUsage(req, channelID, status, tokens)
		if h.db != nil && h.db.ProxyLog != nil && requestID != "" {
			if err := h.db.ProxyLog.UpdateMetaByRequestID(requestID, firstByteMs, clientFamily); err != nil {
				log.Printf("relay: update log meta request_id=%s: %v", requestID, err)
			}
		}
		if h.liveTrace != nil && requestID != "" {
			h.liveTrace.FinishCopied(requestID, status, int64(firstByteMs), bytesSent, tokens.PromptTokens, tokens.CompletionTokens)
		}
	}
}

// finalizeFailedLiveTrace closes the live view for an attempt that will never
// reach the accounting callback: no upstream response at all, or an error.
//
// Success is deliberately left running here — `attemptAccountant` finalizes it
// after the body has been copied, so a stream keeps reporting byte and
// first-byte progress until the client has all of it.
func (h *RelayHandler) finalizeFailedLiveTrace(requestID string, result *relay.Result, canceled bool) {
	if h.liveTrace == nil || requestID == "" {
		return
	}
	switch {
	case result == nil:
		h.liveTrace.Finish(requestID, livetrace.StatusFailed, "upstream response missing")
	case result.Err != nil && canceled:
		h.liveTrace.Finish(requestID, livetrace.StatusCanceled, result.Err.Error())
	case result.Err != nil:
		h.liveTrace.Finish(requestID, livetrace.StatusFailed, result.Err.Error())
	}
}
