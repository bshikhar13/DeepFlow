// Package provider is DeepFlow's model-agnostic inference layer.
package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
)

// Role is the logical responsibility a request serves.
type Role string

const (
	RoleDialogue    Role = "dialogue"
	RoleEvaluator   Role = "evaluator"
	RoleCell        Role = "cell"
	RoleConsolidate Role = "consolidate"
)

// Turn is one conversation message.
type Turn struct {
	Role    string
	Content string
}

// TextRequest asks for streamed free text.
type TextRequest struct {
	Role      Role
	System    string
	Messages  []Turn
	MaxTokens int
}

// StructuredRequest asks for JSON matching Schema.
type StructuredRequest struct {
	Role      Role
	Name      string
	System    string
	Prompt    string
	Schema    map[string]any
	MaxTokens int
}

// Usage reports tokens consumed by a call.
type Usage struct {
	InputTokens, OutputTokens int64
}

// Provider generates model output.
type Provider interface {
	Name() string
	Stream(ctx context.Context, req TextRequest, onDelta func(string)) (Usage, error)
	Structured(ctx context.Context, req StructuredRequest, out any) (Usage, error)
}

var (
	// ErrRefused means the model declined the request.
	ErrRefused = errors.New("model declined the request")
	// ErrMalformed means the model's output was not valid for the schema.
	ErrMalformed = errors.New("model returned malformed output")
	// ErrTruncated means the output hit the token limit.
	ErrTruncated = errors.New("model output was truncated")
	// ErrNoRoute means no host for the model meets the request's requirements.
	ErrNoRoute = errors.New("no available host meets the requirements")
	// ErrAccount means the service rejected the key or has no credit left.
	ErrAccount = errors.New("the model service rejected the key or has no credit")
)

// DefaultModel is the Claude model used unless DEEPFLOW_MODEL overrides it.
const DefaultModel = "claude-opus-5-5"

// Remote is implemented by providers that send learning content off this
// computer, so the app can tell the learner where it goes.
type Remote interface {
	Vendor() string
	ModelName() string
}

// FromEnv selects a provider from the environment. DEEPFLOW_PROVIDER chooses
// one explicitly; otherwise the first API key found decides, then the Mock.
// Asking for a provider that can't run is an error rather than a quiet fallback
// to canned content.
func FromEnv() (Provider, error) {
	anthropicKey := os.Getenv("ANTHROPIC_API_KEY")
	openaiKey := os.Getenv("OPENAI_API_KEY")
	routerKey := os.Getenv("OPENROUTER_API_KEY")
	model := os.Getenv("DEEPFLOW_MODEL")
	choice := os.Getenv("DEEPFLOW_PROVIDER")
	if choice == "" {
		switch {
		case anthropicKey != "":
			choice = "claude"
		case openaiKey != "":
			choice = "openai"
		case routerKey != "":
			choice = "openrouter"
		default:
			choice = "mock"
		}
	}
	switch choice {
	case "mock":
		return NewMock(), nil
	case "claude":
		if anthropicKey == "" {
			return nil, errors.New("DEEPFLOW_PROVIDER=claude needs ANTHROPIC_API_KEY")
		}
		if model == "" {
			model = DefaultModel
		}
		return NewClaude(anthropicKey, model, ""), nil
	case "openai":
		if openaiKey == "" {
			return nil, errors.New("DEEPFLOW_PROVIDER=openai needs OPENAI_API_KEY")
		}
		if model == "" {
			model = DefaultOpenAIModel
		}
		return NewOpenAI(openaiKey, model, ""), nil
	case "openrouter":
		if routerKey == "" {
			return nil, errors.New("DEEPFLOW_PROVIDER=openrouter needs OPENROUTER_API_KEY")
		}
		if model == "" {
			model = DefaultOpenRouterModel
		}
		return NewOpenRouter(routerKey, model, ""), nil
	}
	return nil, fmt.Errorf("unknown DEEPFLOW_PROVIDER %q: use claude, openai, openrouter or mock", choice)
}
