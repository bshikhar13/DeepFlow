import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { ConsentNotice } from "../views/ConsentNotice";
import { ProviderBadge } from "./Header";

const openai = { provider: "openai", vendor: "OpenAI", model: "gpt-6.1-sol", consentRequired: true };
const claude = { provider: "claude", vendor: "Anthropic", model: "claude-opus-5-5", consentRequired: true };

const router = { provider: "openrouter", vendor: "OpenRouter", model: "anthropic/claude-sonnet-5.5", consentRequired: true };

describe("data disclosure", () => {
  it("says OpenRouter passes data on to the model's maker", () => {
    render(<ConsentNotice status={router} onAccept={() => {}} />);
    expect(screen.getByText(/sends parts of what you write/)).toHaveTextContent("OpenRouter");
    expect(screen.getByText(/passes it on/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Continue with OpenRouter" })).toBeInTheDocument();
  });
  it("names the provider that actually receives the data", () => {
    render(<ConsentNotice status={openai} onAccept={() => {}} />);
    expect(screen.getByText(/sends parts of what you write/)).toHaveTextContent("OpenAI");
    expect(screen.getByText(/sends parts of what you write/)).not.toHaveTextContent("Anthropic");
    expect(screen.getByRole("button", { name: "Continue with OpenAI" })).toBeInTheDocument();
  });
  it("shows the model and destination in the badge", () => {
    render(<ProviderBadge status={openai} />);
    expect(screen.getByText("gpt-6.1-sol · sends to OpenAI")).toBeInTheDocument();
  });
  it("keeps naming Anthropic for Claude", () => {
    render(<ProviderBadge status={claude} />);
    expect(screen.getByText("claude-opus-5-5 · sends to Anthropic")).toBeInTheDocument();
  });
  it("labels the mock model as local", () => {
    render(<ProviderBadge status={{ provider: "mock", vendor: "", model: "", consentRequired: false }} />);
    expect(screen.getByText("Mock model")).toBeInTheDocument();
  });
});
