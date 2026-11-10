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

type orCaptured struct {
	body   map[string]any
	path   string
	header http.Header
}

func orChunk(b *strings.Builder, delta, finish string, usage bool) {
	f := "null"
	if finish != "" {
		f = fmt.Sprintf("%q", finish)
	}
	u := ""
	if usage {
		u = `,"usage":{"prompt_tokens":7,"completion_tokens":3,"total_tokens":10,"completion_tokens_details":{"reasoning_tokens":1}}`
	}
	fmt.Fprintf(b, `data: {"id":"x","object":"chat.completion.chunk","created":1,"model":"m","choices":[{"index":0,"delta":%s,"finish_reason":%s}]%s}`+"\n\n", delta, f, u)
}

func orText(text, finish string) string {
	q, _ := json.Marshal(text)
	var b strings.Builder
	// OpenRouter sends these comments to keep a slow connection alive.
	b.WriteString(": OPENROUTER PROCESSING\n\n")
	orChunk(&b, `{"role":"assistant","content":`+string(q)+`}`, "", false)
	orChunk(&b, `{"content":""}`, finish, true)
	b.WriteString("data: [DONE]\n\n")
	return b.String()
}

func fakeOpenRouter(t *testing.T, status int, body string) (*OpenRouter, *orCaptured) {
	t.Helper()
	c := &orCaptured{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		json.Unmarshal(b, &c.body)
		c.path, c.header = r.URL.Path, r.Header.Clone()
		if status != 200 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			io.WriteString(w, body)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return NewOpenRouter("test-key", DefaultOpenRouterModel, srv.URL), c
}

func TestOpenRouterStructuredRequestShape(t *testing.T) {
	p, c := fakeOpenRouter(t, 200, orText(`{"ok":true}`, "stop"))
	var out map[string]any
	u, err := p.Structured(context.Background(), StructuredRequest{Role: RoleEvaluator, Name: "evaluator.assess", System: "sys", Prompt: "p", Schema: map[string]any{"type": "object"}}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if out["ok"] != true || u.InputTokens != 7 || u.OutputTokens != 3 {
		t.Fatalf("out %v usage %+v", out, u)
	}
	if !strings.HasSuffix(c.path, "/chat/completions") || c.header.Get("Authorization") != "Bearer test-key" {
		t.Errorf("%s %v", c.path, c.header.Get("Authorization"))
	}
	if c.body["model"] != DefaultOpenRouterModel || c.body["stream"] != true {
		t.Errorf("%v", c.body)
	}
	if v, _ := c.body["max_tokens"].(float64); v < 32000 {
		t.Errorf("max_tokens %v: reasoning shares this limit", c.body["max_tokens"])
	}
	msgs := c.body["messages"].([]any)
	if len(msgs) != 2 || msgs[0].(map[string]any)["role"] != "system" || msgs[0].(map[string]any)["content"] != "sys" {
		t.Errorf("%v", msgs)
	}
	if r := c.body["reasoning"].(map[string]any); r["effort"] != "low" {
		t.Errorf("reasoning %v", r)
	}
	route := c.body["provider"].(map[string]any)
	if route["require_parameters"] != true {
		t.Error("must only route to endpoints that support structured output")
	}
	if route["data_collection"] != "deny" {
		t.Error("students' content must not go to endpoints that may train on it")
	}
	js := c.body["response_format"].(map[string]any)["json_schema"].(map[string]any)
	if js["strict"] != true || js["schema"] == nil || strings.Contains(js["name"].(string), ".") {
		t.Errorf("%v", js)
	}
	if _, ok := c.body["temperature"]; ok {
		t.Error("temperature must not be sent")
	}
}

func TestOpenRouterStreamsDialogue(t *testing.T) {
	p, c := fakeOpenRouter(t, 200, orText("hello", "stop"))
	var got string
	_, err := p.Stream(context.Background(), TextRequest{Role: RoleDialogue, Messages: []Turn{{Role: "user", Content: "q"}, {Role: "assistant", Content: "a"}, {Role: "user", Content: "r"}}}, func(s string) { got += s })
	if err != nil || got != "hello" {
		t.Fatalf("%q %v", got, err)
	}
	if r := c.body["reasoning"].(map[string]any); r["effort"] != "medium" {
		t.Errorf("reasoning %v", r)
	}
	msgs := c.body["messages"].([]any)
	if len(msgs) != 3 || msgs[1].(map[string]any)["role"] != "assistant" {
		t.Errorf("%v", msgs)
	}
}

func TestOpenRouterTruncationSaysWhy(t *testing.T) {
	p, _ := fakeOpenRouter(t, 200, orText(`{"a":`, "length"))
	var out map[string]any
	_, err := p.Structured(context.Background(), StructuredRequest{Name: "n", Schema: map[string]any{}}, &out)
	if !errors.Is(err, ErrTruncated) || !strings.Contains(err.Error(), "3 output tokens") || !strings.Contains(err.Error(), "1 of them reasoning") {
		t.Fatalf("got %v", err)
	}
}

func TestOpenRouterContentFilterReturnsErrRefused(t *testing.T) {
	p, _ := fakeOpenRouter(t, 200, orText("", "content_filter"))
	var out map[string]any
	if _, err := p.Structured(context.Background(), StructuredRequest{Name: "n", Schema: map[string]any{}}, &out); !errors.Is(err, ErrRefused) {
		t.Fatalf("got %v", err)
	}
}

func TestOpenRouterRefusalReturnsErrRefused(t *testing.T) {
	var b strings.Builder
	orChunk(&b, `{"refusal":"no"}`, "stop", true)
	p, _ := fakeOpenRouter(t, 200, b.String())
	var out map[string]any
	if _, err := p.Structured(context.Background(), StructuredRequest{Name: "n", Schema: map[string]any{}}, &out); !errors.Is(err, ErrRefused) {
		t.Fatalf("got %v", err)
	}
}

func TestOpenRouterInvalidJSONReturnsErrMalformed(t *testing.T) {
	p, _ := fakeOpenRouter(t, 200, orText("not json", "stop"))
	var out map[string]any
	if _, err := p.Structured(context.Background(), StructuredRequest{Name: "n", Schema: map[string]any{}}, &out); !errors.Is(err, ErrMalformed) {
		t.Fatalf("got %v", err)
	}
}

// OpenRouter reports upstream failures inside a 200 stream.
func TestOpenRouterMidStreamErrorIsReported(t *testing.T) {
	var b strings.Builder
	orChunk(&b, `{"content":"par"}`, "", false)
	b.WriteString(`data: {"id":"x","object":"chat.completion.chunk","created":1,"model":"m","error":{"code":502,"message":"upstream died"},"choices":[{"index":0,"delta":{"content":""},"finish_reason":"error"}]}` + "\n\n")
	p, _ := fakeOpenRouter(t, 200, b.String())
	_, err := p.Stream(context.Background(), TextRequest{Messages: []Turn{{Role: "user", Content: "q"}}}, func(string) {})
	if err == nil {
		t.Fatal("want error")
	}
	if !strings.Contains(err.Error(), "upstream died") {
		t.Errorf("should say why: %v", err)
	}
}

func TestOpenRouterAccountProblemsAreNamed(t *testing.T) {
	for _, status := range []int{401, 402} {
		p, _ := fakeOpenRouter(t, status, `{"error":{"code":`+fmt.Sprint(status)+`,"message":"no"}}`)
		var out map[string]any
		_, err := p.Structured(context.Background(), StructuredRequest{Name: "n", Schema: map[string]any{}}, &out)
		if !errors.Is(err, ErrAccount) {
			t.Errorf("%d: got %v", status, err)
		}
	}
}

func TestOpenRouterEmptyReplyIsMalformed(t *testing.T) {
	p, _ := fakeOpenRouter(t, 200, orText("", "stop"))
	var out map[string]any
	if _, err := p.Structured(context.Background(), StructuredRequest{Name: "n", Schema: map[string]any{}}, &out); !errors.Is(err, ErrMalformed) {
		t.Fatalf("got %v", err)
	}
}

func setORKey(t *testing.T, provider, or, anthropic, openai, model string) {
	t.Helper()
	setEnv(t, provider, anthropic, openai, model)
	t.Setenv("OPENROUTER_API_KEY", or)
}

func TestFromEnvOpenRouter(t *testing.T) {
	setORKey(t, "", "k", "", "", "")
	p, err := FromEnv()
	if err != nil || p.Name() != "openrouter" {
		t.Fatalf("%v %v", p, err)
	}
	if p.(Remote).Vendor() != "OpenRouter" || p.(Remote).ModelName() != DefaultOpenRouterModel {
		t.Fatalf("%v", p)
	}
	setORKey(t, "openrouter", "k", "a", "o", "google/gemini-3.5-flash")
	p, _ = FromEnv()
	if p.Name() != "openrouter" || p.(Remote).ModelName() != "google/gemini-3.5-flash" {
		t.Fatalf("%v", p)
	}
	setORKey(t, "openrouter", "", "a", "o", "")
	if _, err := FromEnv(); err == nil {
		t.Fatal("no key should fail rather than fall back")
	}
	setORKey(t, "", "k", "", "o", "")
	if p, _ := FromEnv(); p.Name() != "openai" {
		t.Fatalf("existing keys keep their order: %v", p.Name())
	}
}

func TestOpenRouterDefaultsToQwen(t *testing.T) {
	if DefaultOpenRouterModel != "qwen/qwen3.8-max-0902" {
		t.Fatal(DefaultOpenRouterModel)
	}
}

func TestOpenRouterNoEndpointIsNamed(t *testing.T) {
	p, _ := fakeOpenRouter(t, 404, `{"error":{"code":404,"message":"No endpoints found matching your data policy"}}`)
	var out map[string]any
	_, err := p.Structured(context.Background(), StructuredRequest{Name: "n", Schema: map[string]any{}}, &out)
	if !errors.Is(err, ErrNoRoute) || !strings.Contains(err.Error(), "data policy") {
		t.Fatalf("got %v", err)
	}
}

func TestOpenRouterAcceptsFencedJSON(t *testing.T) {
	for _, text := range []string{"```json\n{\"ok\":true}\n```", "```\n{\"ok\":true}\n```", "  {\"ok\":true}\n"} {
		p, _ := fakeOpenRouter(t, 200, orText(text, "stop"))
		var out map[string]any
		if _, err := p.Structured(context.Background(), StructuredRequest{Name: "n", Schema: map[string]any{}}, &out); err != nil || out["ok"] != true {
			t.Errorf("%q: %v %v", text, out, err)
		}
	}
}
