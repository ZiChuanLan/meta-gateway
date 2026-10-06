package proxy

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	"github.com/lan/meta-gateway/internal/adapters"
	"github.com/lan/meta-gateway/internal/relay"
)

// forward_convert.go turns an upstream 2xx body into the contract the client
// asked for. It was the second ~130-line block of ForwardWithMeta's key loop and
// is a pure function of the attempt's plan plus the response: stream policy
// folding/expanding, the N×M translation pair's Response/Stream modes, the
// composed adapter's WrapStream, the channel's response mapping, and the
// empty-success check that turns a 2xx non-answer into a failover.

// convertInput is the plan slice this conversion needs. Passed as a struct so
// the call site reads as a list of facts rather than seven positional booleans.
type convertInput struct {
	adapter                 adapters.ForwardAdapter
	channelMap              UpstreamMap
	translation             *adapters.Translation
	effectivePath           string
	aggregateUpstreamStream bool
	synthesizeClientStream  bool
	// clientStream is the DOWNSTREAM choice, which decides the response shape:
	// the stream policy above may have made the upstream speak differently.
	clientStream bool
}

// convertSuccessBody rewrites the result of a 2xx attempt in place. Anything that
// is not a successful response with a body is returned untouched.
func convertSuccessBody(result *relay.Result, in convertInput) *relay.Result {
	if result == nil || result.Err != nil || result.StatusCode < 200 || result.StatusCode >= 300 || result.Body == nil {
		return result
	}
	// Stream policy: fold a forced upstream stream into one completion, or expand
	// a forced non-stream answer into a single-chunk SSE replay — both before the
	// protocol translation below, which then sees the shape it expects.
	if in.aggregateUpstreamStream {
		aggregated, aggErr := aggregateChatStream(result.Body)
		_ = result.Body.Close()
		if aggErr != nil {
			result = &relay.Result{StatusCode: result.StatusCode, Header: result.Header, LatencyMs: result.LatencyMs, Err: fmt.Errorf("stream aggregation failed: %w", aggErr)}
		} else {
			result.Body = io.NopCloser(bytes.NewReader(aggregated))
			if result.Header == nil {
				result.Header = make(http.Header)
			}
			result.Header.Set("Content-Type", "application/json")
		}
	} else if in.synthesizeClientStream {
		raw, readErr := readResponseBody(result.Body, preserveBodyReadLimit)
		_ = result.Body.Close()
		var synthesized []byte
		if readErr == nil {
			synthesized, readErr = synthesizeStreamFromCompletion(raw)
		}
		if readErr != nil {
			result = &relay.Result{StatusCode: result.StatusCode, Header: result.Header, LatencyMs: result.LatencyMs, Err: fmt.Errorf("stream synthesis failed: %w", readErr)}
		} else {
			result.Body = io.NopCloser(bytes.NewReader(synthesized))
			if result.Header == nil {
				result.Header = make(http.Header)
			}
			result.Header.Set("Content-Type", "text/event-stream")
		}
	}
	// N×M matrix path: the (anthropic → family) pair's Response/Stream modes
	// convert upstream output back to the Anthropic contract.
	//
	// A failed stream-policy conversion above replaced result with an error that
	// carries NO body, so the outer body check no longer holds here: every branch
	// below must re-verify it or it reads from a nil body and panics.
	convertible := result.Err == nil && result.Body != nil
	if convertible && in.translation != nil {
		if in.clientStream && in.translation.Stream != nil {
			wrapped, wrapErr := in.translation.Stream(in.effectivePath, result.Body)
			if wrapErr != nil {
				_ = result.Body.Close()
				result = &relay.Result{Header: result.Header, LatencyMs: result.LatencyMs, Err: wrapErr}
			} else {
				result.Body = wrapped
				if result.Header == nil {
					result.Header = make(http.Header)
				}
				if !isBinaryResponsePath(in.effectivePath) {
					result.Header.Set("Content-Type", "text/event-stream")
				}
			}
		} else if !in.clientStream && in.translation.Response != nil {
			raw, readErr := readResponseBody(result.Body, preserveBodyReadLimit)
			_ = result.Body.Close()
			if readErr != nil {
				result = &relay.Result{StatusCode: result.StatusCode, Header: result.Header, LatencyMs: result.LatencyMs, Err: readErr}
			} else if converted, convErr := in.translation.Response(in.effectivePath, raw); convErr != nil {
				result = &relay.Result{
					StatusCode: adapterErrorStatus(convErr, http.StatusBadGateway),
					Header:     result.Header,
					LatencyMs:  result.LatencyMs,
					Err:        fmt.Errorf("proxy: anthropic response: %w", convErr),
				}
			} else {
				result.Body = io.NopCloser(bytes.NewReader(converted))
				if result.Header == nil {
					result.Header = make(http.Header)
				}
				result.Header.Set("Content-Type", transformedContentType(in.effectivePath, in.adapter.Name(), result.Header.Get("Content-Type")))
			}
		}
	} else if convertible && in.clientStream {
		// Reshape native/upstream SSE into the downstream contract (the composed
		// adapter pivots through OpenAI SSE internally).
		wrapped, wrapErr := in.adapter.WrapStream(in.effectivePath, result.Body)
		if wrapErr != nil {
			// The upstream stream is not handed to the client; close it so the
			// connection returns to the pool instead of leaking.
			_ = result.Body.Close()
			result = &relay.Result{Header: result.Header, LatencyMs: result.LatencyMs, Err: wrapErr}
		} else {
			result.Body = wrapped
			if result.Header == nil {
				result.Header = make(http.Header)
			}
			if !isBinaryResponsePath(in.effectivePath) {
				result.Header.Set("Content-Type", "text/event-stream")
			}
		}
	} else if convertible {
		raw, readErr := readResponseBody(result.Body, preserveBodyReadLimit)
		_ = result.Body.Close()
		if readErr != nil {
			result = &relay.Result{StatusCode: result.StatusCode, Header: result.Header, LatencyMs: result.LatencyMs, Err: readErr}
		} else if converted, convErr := in.channelMap.ReshapeResponse(raw, in.effectivePath, in.adapter); convErr != nil {
			result = &relay.Result{
				StatusCode: adapterErrorStatus(convErr, http.StatusBadGateway),
				Header:     result.Header,
				LatencyMs:  result.LatencyMs,
				Err:        fmt.Errorf("proxy: %s response: %w", in.adapter.Name(), convErr),
			}
		} else {
			// Empty-success check: a 2xx chat completion with no choices or an
			// empty message is a silent upstream failure — fail over instead of
			// returning emptiness.
			if in.effectivePath == "chat/completions" && isEmptyChatSuccess(converted) {
				result = &relay.Result{
					StatusCode: result.StatusCode,
					Header:     result.Header,
					LatencyMs:  result.LatencyMs,
					Err:        ErrEmptyCompletion,
				}
			} else {
				result.Body = io.NopCloser(bytes.NewReader(converted))
				if result.Header == nil {
					result.Header = make(http.Header)
				}
				result.Header.Set("Content-Type", transformedContentType(in.effectivePath, in.adapter.Name(), result.Header.Get("Content-Type")))
			}
		}
	}
	return result
}
