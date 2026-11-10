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

type oaCaptured struct {
	body map[string]any
	path string
}

func oaEvent(b *strings.Builder, typ string, fields string) {
	fmt.Fprintf(b, "event: %s\ndata: {\"type\":%q,\"sequence_number\":1%s}\n\n", typ, typ, fields)
}

const oaUsage = `"usage":{"input_tokens":7,"input_tokens_details":{"cached_tokens":0},"output_tokens":3,"output_tokens_details":{"reasoning_tokens":0},"total_tokens":10}`

func oaText(text string) string {
	q, _ := json.Marshal(text)
	var b strings.Builder
	oaEvent(&b, "response.output_text.delta", `,"item_id":"i","output_index":0,"content_index":0,"logprobs":[],"delta":`+string(q))
	oaEvent(&b, "response.completed", `,"response":{"id":"r","status":"completed",`+oaUsage+`}`)
	return b.String()
}

func fakeOpenAI(t *testing.T, sse string) (*OpenAI, *oaCaptured) {
	t.Helper()
	c := &oaCaptured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &c.body)
		c.path = r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, sse)
	}))
	t.Cleanup(srv.Close)
	return NewOpenAI("test-key", DefaultOpenAIModel, srv.URL), c
}

func TestOpenAIStructuredRequestShape(t *testing.T) {
	p, c := fakeOpenAI(t, oaText(`{"ok":true}`))
	var out map[string]any
	u, err := p.Structured(context.Background(), StructuredRequest{Role: RoleEvaluator, Name: "evaluator.assess", System: "sys", Prompt: "p", Schema: map[string]any{"type": "object"}}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true || u.InputTokens != 7 || u.OutputTokens != 3 {
		t.Fatalf("out %v usage %+v", out, u)
	}
	if !strings.HasSuffix(c.path, "/responses") {
		t.Errorf("path %s", c.path)
	}
	if c.body["model"] != "gpt-6.1-sol" || c.body["instructions"] != "sys" || c.body["stream"] != true {
		t.Errorf("%v", c.body)
	}
	if r := c.body["reasoning"].(map[string]any); r["effort"] != "low" {
		t.Errorf("reasoning %v", r)
	}
	if c.body["store"] != false {
		t.Errorf("learning content must not be stored by default: %v", c.body["store"])
	}
	f := c.body["text"].(map[string]any)["format"].(map[string]any)
	if f["type"] != "json_schema" || f["strict"] != true || f["schema"] == nil {
		t.Errorf("format %v", f)
	}
	// OpenAI rejects dots in schema names, and DeepFlow's request names have them.
	if name := f["name"].(string); strings.Contains(name, ".") || name == "" {
		t.Errorf("name %q", name)
	}
	if _, ok := c.body["temperature"]; ok {
		t.Error("temperature must not be sent")
	}
}

func TestOpenAIStreamsDialogueWithHistory(t *testing.T) {
	p, c := fakeOpenAI(t, oaText("hello"))
	var got string
	_, err := p.Stream(context.Background(), TextRequest{Role: RoleDialogue, Messages: []Turn{{Role: "user", Content: "q"}, {Role: "assistant", Content: "a"}, {Role: "user", Content: "r"}}}, func(s string) { got += s })
	if err != nil || got != "hello" {
		t.Fatalf("%q %v", got, err)
	}
	if r := c.body["reasoning"].(map[string]any); r["effort"] != "medium" {
		t.Errorf("reasoning %v", r)
	}
	in := c.body["input"].([]any)
	if len(in) != 3 || in[1].(map[string]any)["role"] != "assistant" || in[1].(map[string]any)["content"] != "a" {
		t.Errorf("input %v", in)
	}
}

func TestOpenAIRefusalReturnsErrRefused(t *testing.T) {
	var b strings.Builder
	oaEvent(&b, "response.refusal.done", `,"item_id":"i","output_index":0,"content_index":0,"refusal":"no"`)
	oaEvent(&b, "response.completed", `,"response":{"id":"r","status":"completed",`+oaUsage+`}`)
	p, _ := fakeOpenAI(t, b.String())
	var out map[string]any
	if _, err := p.Structured(context.Background(), StructuredRequest{Name: "n", Schema: map[string]any{}}, &out); !errors.Is(err, ErrRefused) {
		t.Fatalf("got %v", err)
	}
}

func incomplete(reason string) string {
	var b strings.Builder
	oaEvent(&b, "response.output_text.delta", `,"item_id":"i","output_index":0,"content_index":0,"logprobs":[],"delta":"{\"a\":"`)
	oaEvent(&b, "response.incomplete", `,"response":{"id":"r","status":"incomplete","incomplete_details":{"reason":"`+reason+`"},`+oaUsage+`}`)
	return b.String()
}

func TestOpenAIMaxTokensReturnsErrTruncated(t *testing.T) {
	p, _ := fakeOpenAI(t, incomplete("max_output_tokens"))
	var out map[string]any
	if _, err := p.Structured(context.Background(), StructuredRequest{Name: "n", Schema: map[string]any{}}, &out); !errors.Is(err, ErrTruncated) {
		t.Fatalf("got %v", err)
	}
}

func TestOpenAITruncationSaysWhy(t *testing.T) {
	p, _ := fakeOpenAI(t, incomplete("max_output_tokens"))
	var out map[string]any
	_, err := p.Structured(context.Background(), StructuredRequest{Name: "n", Schema: map[string]any{}}, &out)
	if !errors.Is(err, ErrTruncated) || !strings.Contains(err.Error(), "max_output_tokens") || !strings.Contains(err.Error(), "3 output tokens") {
		t.Fatalf("got %v", err)
	}
}

func TestOpenAIStructuredLeavesRoomForReasoning(t *testing.T) {
	p, c := fakeOpenAI(t, oaText(`{"ok":true}`))
	var out map[string]any
	if _, err := p.Structured(context.Background(), StructuredRequest{Name: "n", Schema: map[string]any{}}, &out); err != nil {
		t.Fatal(err)
	}
	if v, _ := c.body["max_output_tokens"].(float64); v < 32000 {
		t.Fatalf("limit %v: reasoning tokens count against it", v)
	}
}

func TestOpenAIContentFilterReturnsErrRefused(t *testing.T) {
	p, _ := fakeOpenAI(t, incomplete("content_filter"))
	var out map[string]any
	if _, err := p.Structured(context.Background(), StructuredRequest{Name: "n", Schema: map[string]any{}}, &out); !errors.Is(err, ErrRefused) {
		t.Fatalf("got %v", err)
	}
}

func TestOpenAIInvalidJSONReturnsErrMalformed(t *testing.T) {
	p, _ := fakeOpenAI(t, oaText("not json"))
	var out map[string]any
	if _, err := p.Structured(context.Background(), StructuredRequest{Name: "n", Schema: map[string]any{}}, &out); !errors.Is(err, ErrMalformed) {
		t.Fatalf("got %v", err)
	}
}

func TestOpenAIFailedResponseReturnsError(t *testing.T) {
	var b strings.Builder
	oaEvent(&b, "response.failed", `,"response":{"id":"r","status":"failed","error":{"code":"server_error","message":"boom"}}`)
	p, _ := fakeOpenAI(t, b.String())
	_, err := p.Stream(context.Background(), TextRequest{Messages: []Turn{{Role: "user", Content: "q"}}}, func(string) {})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("got %v", err)
	}
}

func TestOpenAIStreamErrorEventReturnsError(t *testing.T) {
	var b strings.Builder
	oaEvent(&b, "error", `,"code":"rate_limit","message":"slow down","param":""`)
	p, _ := fakeOpenAI(t, b.String())
	_, err := p.Stream(context.Background(), TextRequest{Messages: []Turn{{Role: "user", Content: "q"}}}, func(string) {})
	if err == nil {
		t.Fatal("want error")
	}
}

func setEnv(t *testing.T, provider, anthropic, openai, model string) {
	t.Helper()
	t.Setenv("OPENROUTER_API_KEY", "")
	t.Setenv("DEEPFLOW_PROVIDER", provider)
	t.Setenv("ANTHROPIC_API_KEY", anthropic)
	t.Setenv("OPENAI_API_KEY", openai)
	t.Setenv("DEEPFLOW_MODEL", model)
}

func TestFromEnvSelection(t *testing.T) {
	cases := []struct {
		name, provider, anthropic, openai, want string
	}{
		{"nothing set is mock", "", "", "", "mock"},
		{"anthropic key is claude", "", "k", "", "claude"},
		{"openai key is openai", "", "", "k", "openai"},
		{"both keys prefer claude", "", "k", "k", "claude"},
		{"explicit openai beats claude key", "openai", "k", "k", "openai"},
		{"explicit claude", "claude", "k", "k", "claude"},
		{"mock forced", "mock", "k", "k", "mock"},
	}
	for _, c := range cases {
		setEnv(t, c.provider, c.anthropic, c.openai, "")
		p, err := FromEnv()
		if err != nil || p.Name() != c.want {
			t.Errorf("%s: %v %v", c.name, p, err)
		}
	}
}

// Silently falling back to canned content would be worse than stopping.
func TestFromEnvExplicitProviderWithoutKeyFails(t *testing.T) {
	for _, name := range []string{"openai", "claude"} {
		setEnv(t, name, "", "", "")
		if _, err := FromEnv(); err == nil {
			t.Errorf("%s without a key should fail", name)
		}
	}
	setEnv(t, "gemini", "k", "k", "")
	if _, err := FromEnv(); err == nil {
		t.Error("unknown provider should fail")
	}
}

func TestFromEnvModelDefaultsPerProvider(t *testing.T) {
	setEnv(t, "openai", "", "k", "")
	p, _ := FromEnv()
	if p.(Remote).ModelName() != "gpt-6.1-sol" || p.(Remote).Vendor() != "OpenAI" {
		t.Fatalf("%v", p)
	}
	setEnv(t, "openai", "", "k", "gpt-6-astra")
	p, _ = FromEnv()
	if p.(Remote).ModelName() != "gpt-6-astra" {
		t.Fatal(p.(Remote).ModelName())
	}
	setEnv(t, "claude", "k", "", "")
	p, _ = FromEnv()
	if p.(Remote).ModelName() != "claude-opus-5-5" || p.(Remote).Vendor() != "Anthropic" {
		t.Fatalf("%v", p)
	}
	if _, ok := Provider(NewMock()).(Remote); ok {
		t.Fatal("mock sends nothing anywhere, so it isn't Remote")
	}
}
