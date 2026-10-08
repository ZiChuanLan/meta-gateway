package adapters

import (
	"bufio"
	"io"
	"strings"
)

// SSE plumbing shared by the streaming adapters.
//
// Three of them convert an upstream SSE stream into another protocol's stream —
// Anthropic Messages → chat, chat → Anthropic Messages, chat → Responses — and
// each carried its own copy of the frame loop and of the once-only close. The
// copies had already drifted apart (one collected Anthropic's "event:" line, the
// others dropped it; one relied on a blank line to dispatch a frame and another
// on EOF), which is why both live here now.

// streamSource is the upstream body an adapter reads from, closed once.
//
// Embedding it gives every adapter the same Close. The proxy closes a stream on
// its error path and the caller may close it again; a second Close on a body the
// pool has already taken back is not harmless.
type streamSource struct {
	source io.ReadCloser
	closed bool
}

func newStreamSource(source io.ReadCloser) streamSource {
	return streamSource{source: source}
}

func (s *streamSource) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	if s.source == nil {
		return nil
	}
	return s.source.Close()
}

// sseFrameReader assembles SSE frames: consecutive "data:" lines belong to one
// frame, a blank line dispatches it, and a frame that arrives without a trailing
// newline is still dispatched at EOF.
type sseFrameReader struct {
	reader *bufio.Reader
	// wantEvent collects the frame's "event:" name. Anthropic's Messages
	// contract names its events, so that adapter asks for it; chat and Responses
	// streams carry none and skip the line.
	wantEvent bool
}

func newSSEFrameReader(source io.Reader, wantEvent bool) sseFrameReader {
	return sseFrameReader{reader: bufio.NewReader(source), wantEvent: wantEvent}
}

// next returns the next frame: its event name (empty unless wantEvent) and its
// payload. io.EOF is returned once the stream is exhausted, after any frame that
// preceded it. A blank line with no frame in flight is skipped rather than
// reported, so a caller's loop cannot spin on a keep-alive.
//
// A partial final line is dropped, as every copy of this loop did: no upstream
// this gateway talks to ends a stream without a newline, and dispatching a
// truncated frame would hand the client half a JSON document.
func (r *sseFrameReader) next() (event, data string, err error) {
	hasData := false
	for {
		line, readErr := r.reader.ReadString('\n')
		if readErr != nil {
			if readErr == io.EOF && (hasData || event != "") {
				return event, data, nil
			}
			return "", "", readErr
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			if !hasData && event == "" {
				continue
			}
			return event, data, nil
		}
		if r.wantEvent && strings.HasPrefix(line, "event:") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if hasData {
				data += "\n" + payload
			} else {
				data = payload
				hasData = true
			}
			continue
		}
		// id:/retry:/comment lines are not part of the payload.
	}
}
