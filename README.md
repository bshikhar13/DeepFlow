# DeepFlow

An adaptive learning workspace for school students in classes 6 to 12.

A student picks a topic and their class. DeepFlow builds a workspace of live cards around it. An AI harness watches what the student does in each card and adapts the whole workspace to match.

## What a student sees

| Card | What it does |
|---|---|
| **Chat** | A tutor that keeps replies short and asks one question at a time |
| **My Journey** | The topic's index as a path, with progress toward the next milestone |
| **Quiz** | One question at a time; a new one replaces it after each answer |
| **Game** | A live experiment, sorting game or map hunt that earns stars |
| **Key Idea** | One core idea as a flip card |
| **My Notes** | The ideas the student has formed, kept across sessions |

- **Maths renders properly.** Write `\(Z = 3 + 4i\)` and the card shows it as an equation (KaTeX).
- **Maps for history and geography.** These topics get a Map card and a "find it on the map" game. The bundled Natural Earth outline has no political borders and works offline.
- **The cards share context.** Ask for a game in Chat and the Game card changes to match.
- **Answers stay on the server.** Quiz answers never reach the browser, and the server checks every game win itself.

## Quick start

You need **Go** (it downloads the version named in `go.mod`) and **Node 20+**.

```bash
git clone https://github.com/bshikhar13/DeepFlow.git
cd DeepFlow
make build
./deepflow
```

Open http://127.0.0.1:7878.

With no API key set, DeepFlow runs on a built-in **Mock model**. It gives fixed sample content for force, maths, history, geography and general topics, so you can try the app offline and for free.

## Using a real model

Set one of these keys in your shell before starting. Keys are read only from environment variables, so DeepFlow never saves them.

| Provider | Variable | Default model |
|---|---|---|
| Anthropic Claude | `ANTHROPIC_API_KEY` | `claude-opus-5-5` |
| OpenAI | `OPENAI_API_KEY` | `gpt-6.1-sol` |
| OpenRouter | `OPENROUTER_API_KEY` | `qwen/qwen3.8-max-0902` |

```bash
export OPENROUTER_API_KEY=...   # your key
./deepflow
```

You can pick a different model with `DEEPFLOW_MODEL`:

```bash
DEEPFLOW_MODEL=qwen/qwen3.8-flash ./deepflow
```

If more than one key is set, DeepFlow tries Anthropic, then OpenAI, then OpenRouter. Set `DEEPFLOW_PROVIDER` to choose one.

> An OpenAI key comes from platform.openai.com. A ChatGPT subscription doesn't include API access.

### Privacy

- Before the first model call, DeepFlow asks you to acknowledge where learning content is sent. It asks again for each new provider.
- OpenAI requests are sent with `store: false`.
- On OpenRouter, DeepFlow only uses hosts that don't keep or train on prompts. It also requires hosts that support structured output. If no host meets both rules for the chosen model, its cards show a message asking you to try a different model.
- The server only listens on loopback (`127.0.0.1`). All data stays in a local SQLite file.

## Configuration

| Variable | Default | Purpose |
|---|---|---|
| `DEEPFLOW_PROVIDER` | auto | `claude`, `openai`, `openrouter` or `mock` |
| `DEEPFLOW_MODEL` | per provider | Model used by every agent |
| `DEEPFLOW_HOME` | `~/.deepflow` | Folder for `deepflow.db` |
| `DEEPFLOW_PORT` | `7878` | Port (loopback only) |
| `DEEPFLOW_ADAPT_WINDOW` | `5m` | Minimum gap between changes to the workspace's layout that the student didn't ask for |

## How it works

DeepFlow is a Go server with an embedded React app. Inside the server, a small multi-agent harness runs the workspace:

- **Cell agents**, one per card type, generate that card's content. They return data only, never code.
- **The Evaluator** reads learner events, such as answers, game moves and chat. It records evidence of what the student understands. A guard stops it from claiming more than the events show.
- **The Orchestrator** decides the workspace's structure: which cards appear, where, and when. Its `Decide` function is pure, and it's the only code that can change the layout. When a student asks for a change explicitly, the request skips the rate limit. Every change can be undone.
- **The event bus** saves each event before delivering it. The browser receives events over server-sent events with sequence numbers, so it can catch up after a dropped connection.

```
cmd/deepflow          entry point
internal/harness      event bus, shared context, agent runtime, scheduling, budgets
internal/agents       one agent per card type
internal/evaluator    interprets learner events, guards evidence claims
internal/orchestrator workspace structure, reflow and undo
internal/session      live sessions, consolidation, review questions on return
internal/expr         safe expression language for games (mirrored in web/src/lib/expr.ts)
internal/provider     Claude, OpenAI, OpenRouter and Mock providers
internal/store        SQLite persistence
internal/api          REST and server-sent events
web/                  React + TypeScript + Vite frontend
```

## Development

```bash
make test      # Go and frontend tests
```

For frontend work, run the server and Vite together. Vite proxies `/api` to the server:

```bash
./deepflow
```

```bash
npm --prefix web run dev
```

The original design is in [`docs/superpowers/specs`](docs/superpowers/specs/2026-11-07-deepflow-mvp-design.md). It was written before the school redesign. Where the two differ, the code in `internal/agents` and this README are current.
