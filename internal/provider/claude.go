package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/anthropics/anthropic-sdk-go/v2"
	"github.com/anthropics/anthropic-sdk-go/v2/option"
)

const fallbackBeta = "server-side-fallback-2026-07-01"

// Models that accept the server-side refusal fallback's "default" form.
var fallbackModels = map[string]bool{
	"claude-opus-5-5": true, "claude-opus-5": true, "claude-sonnet-5-5": true, "claude-fable-5-1": true,
}

// Claude calls the Anthropic Messages API.
type Claude struct {
	Model  string
	client anthropic.Client
}

// NewClaude builds a Claude provider. An empty baseURL uses the public API.
func NewClaude(apiKey, model, baseURL string) *Claude {
	opts := []option.RequestOption{option.WithAPIKey(apiKey), option.WithMaxRetries(2)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &Claude{Model: model, client: anthropic.NewClient(opts...)}
}

// Name implements Provider.
func (c *Claude) Name() string { return "claude" }

// Vendor implements Remote.
func (c *Claude) Vendor() string { return "Anthropic" }

// ModelName implements Remote.
func (c *Claude) ModelName() string { return c.Model }

func effortFor(r Role) anthropic.BetaOutputConfigEffort {
	switch r {
	case RoleDialogue, RoleConsolidate:
		return anthropic.BetaOutputConfigEffortMedium
	}
	return anthropic.BetaOutputConfigEffortLow
}

func (c *Claude) params(role Role, system string, maxTokens int, msgs []anthropic.BetaMessageParam) anthropic.BetaMessageNewParams {
	if maxTokens == 0 {
		maxTokens = 32000
	}
	p := anthropic.BetaMessageNewParams{
		Model:        c.Model,
		MaxTokens:    int64(maxTokens),
		Messages:     msgs,
		OutputConfig: anthropic.BetaOutputConfigParam{Effort: effortFor(role)},
	}
	if system != "" {
		p.System = []anthropic.BetaTextBlockParam{{Text: system}}
	}
	if fallbackModels[c.Model] {
		p.Betas = []anthropic.AnthropicBeta{fallbackBeta}
		p.Fallbacks = anthropic.BetaFallbacksParamOfDefault()
	}
	return p
}

// run streams a request, forwarding text and returning the stop reason.
func (c *Claude) run(ctx context.Context, p anthropic.BetaMessageNewParams, onDelta func(string)) (string, Usage, error) {
	stream := c.client.Beta.Messages.NewStreaming(ctx, p)
	defer stream.Close()
	var text strings.Builder
	var usage Usage
	var stop anthropic.BetaStopReason
	for stream.Next() {
		switch ev := stream.Current().AsAny().(type) {
		case anthropic.BetaRawMessageStartEvent:
			usage.InputTokens = ev.Message.Usage.InputTokens
		case anthropic.BetaRawContentBlockDeltaEvent:
			if d, ok := ev.Delta.AsAny().(anthropic.BetaTextDelta); ok {
				text.WriteString(d.Text)
				if onDelta != nil {
					onDelta(d.Text)
				}
			}
		case anthropic.BetaRawMessageDeltaEvent:
			stop = ev.Delta.StopReason
			usage.OutputTokens = ev.Usage.OutputTokens
		}
	}
	if err := stream.Err(); err != nil {
		return text.String(), usage, err
	}
	switch stop {
	case anthropic.BetaStopReasonRefusal:
		return text.String(), usage, ErrRefused
	case anthropic.BetaStopReasonMaxTokens:
		return text.String(), usage, ErrTruncated
	}
	return text.String(), usage, nil
}

// Stream implements Provider.
func (c *Claude) Stream(ctx context.Context, req TextRequest, onDelta func(string)) (Usage, error) {
	msgs := make([]anthropic.BetaMessageParam, 0, len(req.Messages))
	for _, t := range req.Messages {
		if t.Role == "assistant" {
			msgs = append(msgs, anthropic.BetaMessageParam{OfAssistant: &anthropic.BetaAssistantMessageParam{
				Content: []anthropic.BetaContentBlockParamUnion{anthropic.NewBetaTextBlock(t.Content)},
			}})
		} else {
			msgs = append(msgs, anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(t.Content)))
		}
	}
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 32000
	}
	_, u, err := c.run(ctx, c.params(req.Role, req.System, maxTokens, msgs), onDelta)
	return u, err
}

// Structured implements Provider.
func (c *Claude) Structured(ctx context.Context, req StructuredRequest, out any) (Usage, error) {
	p := c.params(req.Role, req.System, req.MaxTokens, []anthropic.BetaMessageParam{
		anthropic.NewBetaUserMessage(anthropic.NewBetaTextBlock(req.Prompt)),
	})
	p.OutputConfig.Format = anthropic.BetaJSONOutputFormatParam{Schema: req.Schema}
	text, u, err := c.run(ctx, p, nil)
	if err != nil {
		return u, err
	}
	if err := json.Unmarshal([]byte(text), out); err != nil {
		return u, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	return u, nil
}
