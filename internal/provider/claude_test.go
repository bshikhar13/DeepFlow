package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type captured struct {
	body   map[string]any
	header http.Header
}

func sse(text, stop string) string {
	var b strings.Builder
	ev := func(name, data string) { fmt.Fprintf(&b, "event: %s\ndata: %s\n\n", name, data) }
	ev("message_start", `{"type":"message_start","message":{"id":"m","type":"message","role":"assistant","content":[],"model":"x","stop_reason":null,"stop_sequence":null,"usage":{"input_tokens":7,"output_tokens":0}}}`)
	ev("content_block_start", `{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`)
	q, _ := json.Marshal(text)
	ev("content_block_delta", `{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":`+string(q)+`}}`)
	ev("content_block_stop", `{"type":"content_block_stop","index":0}`)
	ev("message_delta", `{"type":"message_delta","delta":{"stop_reason":"`+stop+`","stop_sequence":null},"usage":{"output_tokens":3}}`)
	ev("message_stop", `{"type":"message_stop"}`)
	return b.String()
}

func fakeClaude(t *testing.T, text, stop string) (*Claude, *captured) {
	t.Helper()
	c := &captured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &c.body)
		c.header = r.Header.Clone()
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sse(text, stop))
	}))
	t.Cleanup(srv.Close)
	return NewClaude("test-key", "claude-opus-5-5", srv.URL), c
}

func TestClaudeRequestShape(t *testing.T) {
	p, c := fakeClaude(t, `{"ok":true}`, "end_turn")
	var out map[string]any
	u, err := p.Structured(context.Background(), StructuredRequest{Role: RoleEvaluator, Name: "n", System: "sys", Prompt: "p", Schema: map[string]any{"type": "object"}}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true || u.InputTokens != 7 || u.OutputTokens != 3 {
		t.Fatalf("out %v usage %+v", out, u)
	}
	if c.body["model"] != "claude-opus-5-5" {
		t.Errorf("model %v", c.body["model"])
	}
	oc, _ := c.body["output_config"].(map[string]any)
	if oc == nil || oc["effort"] != "low" || oc["format"] == nil {
		t.Errorf("output_config %v", c.body["output_config"])
	}
	for _, banned := range []string{"thinking", "temperature", "tool_choice", "top_p"} {
		if _, ok := c.body[banned]; ok {
			t.Errorf("%s must not be sent", banned)
		}
	}
	if c.body["fallbacks"] != "default" {
		t.Errorf("fallbacks %v", c.body["fallbacks"])
	}
	if !strings.Contains(strings.Join(c.header.Values("anthropic-beta"), ","), "server-side-fallback-2026-07-01") {
		t.Errorf("beta header %v", c.header.Values("anthropic-beta"))
	}
}

func TestClaudeDialogueEffortMediumAndStreams(t *testing.T) {
	p, c := fakeClaude(t, "hello", "end_turn")
	var got string
	_, err := p.Stream(context.Background(), TextRequest{Role: RoleDialogue, Messages: []Turn{{Role: "user", Content: "q"}, {Role: "assistant", Content: "a"}, {Role: "user", Content: "r"}}}, func(s string) { got += s })
	if err != nil || got != "hello" {
		t.Fatalf("%q %v", got, err)
	}
	if oc := c.body["output_config"].(map[string]any); oc["effort"] != "medium" {
		t.Errorf("effort %v", oc["effort"])
	}
	if msgs := c.body["messages"].([]any); len(msgs) != 3 {
		t.Errorf("messages %v", msgs)
	}
}

func TestClaudeRefusalReturnsErrRefused(t *testing.T) {
	p, _ := fakeClaude(t, "", "refusal")
	var out map[string]any
	if _, err := p.Structured(context.Background(), StructuredRequest{Name: "n", Schema: map[string]any{}}, &out); !errors.Is(err, ErrRefused) {
		t.Fatalf("got %v", err)
	}
}

func TestClaudeMaxTokensReturnsErrTruncated(t *testing.T) {
	p, _ := fakeClaude(t, `{"a":`, "max_tokens")
	var out map[string]any
	if _, err := p.Structured(context.Background(), StructuredRequest{Name: "n", Schema: map[string]any{}}, &out); !errors.Is(err, ErrTruncated) {
		t.Fatalf("got %v", err)
	}
}

func TestClaudeInvalidJSONReturnsErrMalformed(t *testing.T) {
	p, _ := fakeClaude(t, `not json`, "end_turn")
	var out map[string]any
	if _, err := p.Structured(context.Background(), StructuredRequest{Name: "n", Schema: map[string]any{}}, &out); !errors.Is(err, ErrMalformed) {
		t.Fatalf("got %v", err)
	}
}
