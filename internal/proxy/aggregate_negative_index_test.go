package proxy

import (
	"strings"
	"testing"
)

// An upstream that reports a negative tool_call index must not take the relay
// down. aggregateChatStream grows the slot slice with
// `for toolCalls == nil || call.Index >= len(toolCalls)`, which stops
// immediately for a negative index and then indexes with it.
func TestAggregateChatStreamNegativeToolCallIndex(t *testing.T) {
	stream := strings.Join([]string{
		`data: {"id":"1","model":"m","choices":[{"delta":{"role":"assistant"}}]}`,
		"",
		`data: {"id":"1","model":"m","choices":[{"delta":{"tool_calls":[{"index":-1,"id":"call_a","type":"function","function":{"name":"f","arguments":"{}"}}]}}]}`,
		"",
		"data: [DONE]",
		"",
		"",
	}, "\n")

	out, err := aggregateChatStream(strings.NewReader(stream))
	if err != nil {
		t.Fatalf("aggregate returned an error instead of surviving: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("aggregate produced no completion")
	}
}
