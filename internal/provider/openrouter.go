package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/shared"
)

const openRouterBaseURL = "https://openrouter.ai/api/v1"

// DefaultOpenRouterModel supports the structured output and reasoning controls
// DeepFlow uses. DEEPFLOW_MODEL overrides it.
const DefaultOpenRouterModel = "qwen/qwen3.8-max-0902"

// OpenRouter calls OpenRouter's OpenAI-compatible Chat Completions API. That API
// works across its model providers, unlike the Responses API.
type OpenRouter struct {
	model  string
	client openai.Client
}

// NewOpenRouter builds an OpenRouter provider. An empty baseURL uses the public API.
func NewOpenRouter(apiKey, model, baseURL string) *OpenRouter {
	if baseURL == "" {
		baseURL = openRouterBaseURL
	}
	return &OpenRouter{model: model, client: openai.NewClient(
		option.WithAPIKey(apiKey), option.WithBaseURL(baseURL), option.WithMaxRetries(2),
		option.WithHeader("X-Title", "DeepFlow"),
	)}
}

// Name implements Provider.
func (o *OpenRouter) Name() string { return "openrouter" }

// Vendor implements Remote.
func (o *OpenRouter) Vendor() string { return "OpenRouter" }

// ModelName implements Remote.
func (o *OpenRouter) ModelName() string { return o.model }

// routing goes on every request. require_parameters avoids endpoints that would
// silently ignore the output schema. Students' content must not reach endpoints
// that may store it or train on it, so data_collection is denied.
func routing(role Role) []option.RequestOption {
	return []option.RequestOption{
		option.WithJSONSet("reasoning", map[string]any{"effort": string(openAIEffort(role))}),
		option.WithJSONSet("provider", map[string]any{"require_parameters": true, "data_collection": "deny"}),
	}
}

func (o *OpenRouter) run(ctx context.Context, role Role, p openai.ChatCompletionNewParams, onDelta func(string)) (string, Usage, error) {
	stream := o.client.Chat.Completions.NewStreaming(ctx, p, routing(role)...)
	defer stream.Close()
	var text strings.Builder
	var usage Usage
	var reasoning int64
	finish := ""
	refused := false
	var streamErr error
	for stream.Next() {
		chunk := stream.Current()
		if chunk.Usage.TotalTokens > 0 {
			usage = Usage{InputTokens: chunk.Usage.PromptTokens, OutputTokens: chunk.Usage.CompletionTokens}
			reasoning = chunk.Usage.CompletionTokensDetails.ReasoningTokens
		}
		if field, ok := chunk.JSON.ExtraFields["error"]; ok {
			var e struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal([]byte(field.Raw()), &e)
			streamErr = fmt.Errorf("openrouter: %s", e.Message)
		}
		for _, c := range chunk.Choices {
			if c.Delta.Content != "" {
				text.WriteString(c.Delta.Content)
				if onDelta != nil {
					onDelta(c.Delta.Content)
				}
			}
			if c.Delta.Refusal != "" {
				refused = true
			}
			if c.FinishReason != "" {
				finish = c.FinishReason
			}
		}
	}
	if err := stream.Err(); err != nil {
		var apiErr *openai.Error
		if errors.As(err, &apiErr) && (apiErr.StatusCode == http.StatusUnauthorized || apiErr.StatusCode == http.StatusPaymentRequired) {
			return text.String(), usage, fmt.Errorf("%w: %v", ErrAccount, err)
		}
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusNotFound {
			// Usually the no-training rule leaving no host for this model. That rule
			// protects students, so it fails visibly instead of being relaxed.
			return text.String(), usage, fmt.Errorf("%w: %s", ErrNoRoute, responseBody(apiErr))
		}
		return text.String(), usage, err
	}
	switch {
	case streamErr != nil:
		return text.String(), usage, streamErr
	case refused || finish == "content_filter":
		return text.String(), usage, ErrRefused
	case finish == "length":
		// Reasoning shares this limit, so say how the tokens were spent.
		return text.String(), usage, fmt.Errorf("%w (length; %d output tokens, %d of them reasoning, %d characters of text)", ErrTruncated, usage.OutputTokens, reasoning, text.Len())
	case finish == "error":
		return text.String(), usage, errors.New("openrouter: the model's provider failed partway through")
	}
	return text.String(), usage, nil
}

func (o *OpenRouter) params(system string, maxTokens int, msgs []openai.ChatCompletionMessageParamUnion) openai.ChatCompletionNewParams {
	all := make([]openai.ChatCompletionMessageParamUnion, 0, len(msgs)+1)
	if system != "" {
		all = append(all, openai.SystemMessage(system))
	}
	all = append(all, msgs...)
	return openai.ChatCompletionNewParams{
		Model:     o.model,
		Messages:  all,
		MaxTokens: openai.Int(int64(maxTokens)),
	}
}

// Stream implements Provider.
func (o *OpenRouter) Stream(ctx context.Context, req TextRequest, onDelta func(string)) (Usage, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 32000
	}
	msgs := make([]openai.ChatCompletionMessageParamUnion, 0, len(req.Messages))
	for _, t := range req.Messages {
		if t.Role == "assistant" {
			msgs = append(msgs, openai.AssistantMessage(t.Content))
		} else {
			msgs = append(msgs, openai.UserMessage(t.Content))
		}
	}
	_, u, err := o.run(ctx, req.Role, o.params(req.System, maxTokens, msgs), onDelta)
	return u, err
}

// Structured implements Provider.
func (o *OpenRouter) Structured(ctx context.Context, req StructuredRequest, out any) (Usage, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 32000 // reasoning tokens count against this too
	}
	p := o.params(req.System, maxTokens, []openai.ChatCompletionMessageParamUnion{openai.UserMessage(req.Prompt)})
	p.ResponseFormat = openai.ChatCompletionNewParamsResponseFormatUnion{
		OfJSONSchema: &shared.ResponseFormatJSONSchemaParam{
			JSONSchema: shared.ResponseFormatJSONSchemaJSONSchemaParam{
				Name:   schemaNameUnsafe.ReplaceAllString(req.Name, "_"),
				Schema: req.Schema,
				Strict: openai.Bool(true),
			},
		},
	}
	text, u, err := o.run(ctx, req.Role, p, nil)
	if err != nil {
		return u, err
	}
	if err := json.Unmarshal([]byte(unfence(text)), out); err != nil {
		return u, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	return u, nil
}

// responseBody returns the service's own explanation for the log. It never
// includes the request, so the API key can't leak this way.
func responseBody(e *openai.Error) string {
	dump := string(e.DumpResponse(true))
	if i := strings.Index(dump, "\r\n\r\n"); i >= 0 {
		return dump[i+4:]
	}
	return e.Error()
}

// unfence removes a Markdown code fence. Strict output varies by host, and some
// models wrap otherwise valid JSON in one.
func unfence(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "```") {
		return s
	}
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[i+1:]
	}
	return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), "```"))
}
