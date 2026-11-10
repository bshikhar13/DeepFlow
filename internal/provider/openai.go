package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/openai/openai-go/v3"
	"github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
	"github.com/openai/openai-go/v3/shared"
)

// DefaultOpenAIModel is OpenAI's cost-balanced tier. Several agents share one
// model, so the flagship's higher price adds up quickly.
const DefaultOpenAIModel = "gpt-6.1-sol"

// OpenAI calls the OpenAI Responses API.
type OpenAI struct {
	model  string
	client openai.Client
}

// NewOpenAI builds an OpenAI provider. An empty baseURL uses the public API.
func NewOpenAI(apiKey, model, baseURL string) *OpenAI {
	opts := []option.RequestOption{option.WithAPIKey(apiKey), option.WithMaxRetries(2)}
	if baseURL != "" {
		opts = append(opts, option.WithBaseURL(baseURL))
	}
	return &OpenAI{model: model, client: openai.NewClient(opts...)}
}

// Name implements Provider.
func (o *OpenAI) Name() string { return "openai" }

// Vendor implements Remote.
func (o *OpenAI) Vendor() string { return "OpenAI" }

// ModelName implements Remote.
func (o *OpenAI) ModelName() string { return o.model }

func openAIEffort(r Role) shared.ReasoningEffort {
	switch r {
	case RoleDialogue, RoleConsolidate:
		return shared.ReasoningEffortMedium
	}
	return shared.ReasoningEffortLow
}

func (o *OpenAI) params(role Role, system string, maxTokens int) responses.ResponseNewParams {
	p := responses.ResponseNewParams{
		Model:           o.model,
		MaxOutputTokens: openai.Int(int64(maxTokens)),
		Reasoning:       shared.ReasoningParam{Effort: openAIEffort(role)},
		// DeepFlow keeps its own history locally, so nothing needs storing remotely.
		Store: openai.Bool(false),
	}
	if system != "" {
		p.Instructions = openai.String(system)
	}
	return p
}

func (o *OpenAI) run(ctx context.Context, p responses.ResponseNewParams, onDelta func(string)) (string, Usage, error) {
	stream := o.client.Responses.NewStreaming(ctx, p)
	defer stream.Close()
	var text strings.Builder
	var usage Usage
	record := func(r responses.Response) {
		usage = Usage{InputTokens: r.Usage.InputTokens, OutputTokens: r.Usage.OutputTokens}
	}
	var outcome error
	for stream.Next() {
		switch ev := stream.Current().AsAny().(type) {
		case responses.ResponseTextDeltaEvent:
			text.WriteString(ev.Delta)
			if onDelta != nil {
				onDelta(ev.Delta)
			}
		case responses.ResponseRefusalDoneEvent:
			outcome = ErrRefused
		case responses.ResponseCompletedEvent:
			record(ev.Response)
		case responses.ResponseIncompleteEvent:
			record(ev.Response)
			if ev.Response.IncompleteDetails.Reason == "content_filter" {
				outcome = ErrRefused
			} else {
				// Reasoning counts against the limit, so say how the tokens were spent.
				outcome = fmt.Errorf("%w (%s; %d output tokens, %d of them reasoning, %d characters of text)", ErrTruncated,
					ev.Response.IncompleteDetails.Reason, ev.Response.Usage.OutputTokens, ev.Response.Usage.OutputTokensDetails.ReasoningTokens, text.Len())
			}
		case responses.ResponseFailedEvent:
			record(ev.Response)
			outcome = fmt.Errorf("openai: %s: %s", ev.Response.Error.Code, ev.Response.Error.Message)
		case responses.ResponseErrorEvent:
			outcome = fmt.Errorf("openai: %s: %s", ev.Code, ev.Message)
		}
	}
	if err := stream.Err(); err != nil {
		var apiErr *openai.Error
		if errors.As(err, &apiErr) || outcome == nil {
			return text.String(), usage, err
		}
	}
	return text.String(), usage, outcome
}

// Stream implements Provider.
func (o *OpenAI) Stream(ctx context.Context, req TextRequest, onDelta func(string)) (Usage, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 32000
	}
	p := o.params(req.Role, req.System, maxTokens)
	items := make(responses.ResponseInputParam, 0, len(req.Messages))
	for _, t := range req.Messages {
		role := responses.EasyInputMessageRoleUser
		if t.Role == "assistant" {
			role = responses.EasyInputMessageRoleAssistant
		}
		items = append(items, responses.ResponseInputItemParamOfMessage(t.Content, role))
	}
	p.Input = responses.ResponseNewParamsInputUnion{OfInputItemList: items}
	_, u, err := o.run(ctx, p, onDelta)
	return u, err
}

var schemaNameUnsafe = regexp.MustCompile(`[^a-zA-Z0-9_-]`)

// Structured implements Provider. DeepFlow's schemas already meet strict mode's
// rules: every property required, and no additional properties.
func (o *OpenAI) Structured(ctx context.Context, req StructuredRequest, out any) (Usage, error) {
	maxTokens := req.MaxTokens
	if maxTokens == 0 {
		maxTokens = 32000 // reasoning tokens count against this too
	}
	p := o.params(req.Role, req.System, maxTokens)
	p.Input = responses.ResponseNewParamsInputUnion{OfString: openai.String(req.Prompt)}
	p.Text = responses.ResponseTextConfigParam{Format: responses.ResponseFormatTextConfigUnionParam{
		OfJSONSchema: &responses.ResponseFormatTextJSONSchemaConfigParam{
			Name:   schemaNameUnsafe.ReplaceAllString(req.Name, "_"),
			Schema: req.Schema,
			Strict: openai.Bool(true),
		},
	}}
	text, u, err := o.run(ctx, p, nil)
	if err != nil {
		return u, err
	}
	if err := json.Unmarshal([]byte(text), out); err != nil {
		return u, fmt.Errorf("%w: %v", ErrMalformed, err)
	}
	return u, nil
}
