import { act, fireEvent, render, renderHook, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ApiError, api } from "../api";
import { initialState } from "../state";
import type { Cell, CellType, ConceptObject, GameContent, Message, QuizContent, SessionState } from "../types";
import { Concept } from "./Concept";
import { Dialogue } from "./Dialogue";
import { Document } from "./Document";
import { Game } from "./Game";
import { Journey } from "./Journey";
import { Quiz } from "./Quiz";
import { Thought } from "./Thought";
import { useBusy } from "./useBusy";
import { useDraft } from "./useDraft";

const cell = (type: CellType, content: unknown): Cell => ({
  id: "c1", type, agentInstanceId: "a", objective: "o", status: "waiting", pinned: false, busy: false,
  layout: { col: 1, row: 1, colSpan: 4, rowSpan: 1 }, content, contextVersion: 0, userRevision: 0,
});

const session = (over: Partial<SessionState> = {}): SessionState => ({
  ...initialState(), loaded: true, live: true,
  pursuit: { id: "p1", title: "T", description: "", grade: 7, createdAt: "", updatedAt: "" },
  ...over,
});

const noop = { prefill: "", clearPrefill: () => {}, discuss: () => {} };

beforeEach(() => {
  vi.restoreAllMocks();
  vi.spyOn(api, "draft").mockResolvedValue(undefined);
  vi.spyOn(api, "busy").mockResolvedValue(undefined);
});
afterEach(() => { vi.useRealTimers(); });

const question: QuizContent = { id: "q1", prompt: "What is a force?", options: ["A push", "A colour", "A sound"], topic: "Forces", level: 1, streak: 2, right: 5 };

describe("Quiz", () => {
  it("shows one question and sends the chosen answer", async () => {
    const action = vi.spyOn(api, "action").mockResolvedValue(undefined);
    render(<Quiz cell={cell("quiz", question)} session={session()} {...noop} />);
    expect(screen.getByText("What is a force?")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "A colour" }));
    expect(action).toHaveBeenCalledWith("c1", "quiz.answered", { questionId: "q1", choice: 1 });
  });
  it("shows the right answer and a short reason", () => {
    const answered = { ...question, result: { correct: false, chosen: 1, answer: 0, why: "A force pushes or pulls." } };
    render(<Quiz cell={cell("quiz", answered)} session={session()} {...noop} />);
    expect(screen.getByText("A force pushes or pulls.")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: /A push/ })).toHaveClass("option-right");
    expect(screen.getByRole("button", { name: /A colour/ })).toHaveClass("option-wrong");
    expect(screen.getByRole("button", { name: /A sound/ })).toBeDisabled();
  });
  it("moves to a new question by itself after a correct answer", () => {
    vi.useFakeTimers();
    const action = vi.spyOn(api, "action").mockResolvedValue(undefined);
    const answered = { ...question, result: { correct: true, chosen: 0, answer: 0, why: "Yes." } };
    render(<Quiz cell={cell("quiz", answered)} session={session()} {...noop} />);
    expect(action).not.toHaveBeenCalled();
    act(() => { vi.advanceTimersByTime(3000); });
    expect(action).toHaveBeenCalledWith("c1", "quiz.next", {});
    expect(action).toHaveBeenCalledTimes(1);
  });
  it("waits for the student after a wrong answer", async () => {
    const action = vi.spyOn(api, "action").mockResolvedValue(undefined);
    const answered = { ...question, result: { correct: false, chosen: 1, answer: 0, why: "No." } };
    render(<Quiz cell={cell("quiz", answered)} session={session()} {...noop} />);
    await new Promise((r) => setTimeout(r, 30));
    expect(action).not.toHaveBeenCalled();
    await userEvent.click(screen.getByRole("button", { name: /next question/i }));
    expect(action).toHaveBeenCalledWith("c1", "quiz.next", {});
  });
  it("shows the streak", () => {
    render(<Quiz cell={cell("quiz", question)} session={session()} {...noop} />);
    expect(screen.getByLabelText("Streak: 2")).toBeInTheDocument();
  });
});

const target: GameContent = {
  kind: "target", title: "Push the cart", goal: "Hit the target.",
  params: [{ id: "f", label: "Push", min: 0, max: 100, step: 1, value: 10, unit: "N" }, { id: "m", label: "Mass", min: 1, max: 20, step: 1, value: 10, unit: "kg" }],
  output: { label: "Acceleration", expr: "f/m", unit: "m/s²" },
  levels: [{ target: 5, tolerance: 0.2, hint: "Push harder." }, { target: 2, tolerance: 0.1, hint: "" }],
  note: "Ignores friction.", basis: "illustrative", mode: "none", items: [], groups: [], won: [], stars: 0,
};

describe("Game: hit the target", () => {
  it("updates live with no server call", () => {
    const fetchSpy = vi.spyOn(globalThis, "fetch");
    render(<Game cell={cell("game", target)} session={session()} {...noop} />);
    expect(screen.getByTestId("game-value")).toHaveTextContent("1");
    fireEvent.change(screen.getByLabelText(/Push/), { target: { value: "30" } });
    expect(screen.getByTestId("game-value")).toHaveTextContent("3");
    expect(fetchSpy).not.toHaveBeenCalled();
  });
  it("wins once after holding the target", () => {
    vi.useFakeTimers();
    const action = vi.spyOn(api, "action").mockResolvedValue(undefined);
    render(<Game cell={cell("game", target)} session={session()} {...noop} />);
    fireEvent.change(screen.getByLabelText(/Push/), { target: { value: "50" } });
    expect(action).not.toHaveBeenCalled();
    act(() => { vi.advanceTimersByTime(1000); });
    expect(action).toHaveBeenCalledWith("c1", "game.level_won", { level: 0, values: { f: 50, m: 10 } });
    act(() => { vi.advanceTimersByTime(5000); });
    expect(action).toHaveBeenCalledTimes(1);
  });
  it("passing through the target doesn't count", () => {
    vi.useFakeTimers();
    const action = vi.spyOn(api, "action").mockResolvedValue(undefined);
    render(<Game cell={cell("game", target)} session={session()} {...noop} />);
    fireEvent.change(screen.getByLabelText(/Push/), { target: { value: "50" } });
    act(() => { vi.advanceTimersByTime(100); });
    fireEvent.change(screen.getByLabelText(/Push/), { target: { value: "70" } });
    act(() => { vi.advanceTimersByTime(2000); });
    expect(action).not.toHaveBeenCalled();
  });
  it("moves to the next unfinished level", () => {
    render(<Game cell={cell("game", { ...target, won: [0], stars: 1 })} session={session()} {...noop} />);
    expect(screen.getByText(/Level 2/)).toBeInTheDocument();
    expect(screen.getByTestId("game-target")).toHaveTextContent("2");
  });
  it("labels the model as simplified", () => {
    render(<Game cell={cell("game", target)} session={session()} {...noop} />);
    expect(screen.getByText(/Ignores friction/)).toBeInTheDocument();
  });
  it("copes with an undefined result", () => {
    const g = { ...target, output: { ...target.output, expr: "f/(m-10)" } };
    render(<Game cell={cell("game", g)} session={session()} {...noop} />);
    expect(screen.getByTestId("game-value")).toHaveTextContent("—");
  });
  it("offers a new game when every level is won", async () => {
    const action = vi.spyOn(api, "action").mockResolvedValue(undefined);
    render(<Game cell={cell("game", { ...target, won: [0, 1], stars: 2 })} session={session()} {...noop} />);
    await userEvent.click(screen.getByRole("button", { name: /new game/i }));
    expect(action).toHaveBeenCalledWith("c1", "game.next", {});
  });
});

const order: GameContent = { ...target, kind: "sort", title: "Time machine", goal: "Oldest first.", params: [], levels: [], output: { label: "", expr: "", unit: "" }, basis: "none", mode: "order", items: [{ label: "A", group: "" }, { label: "B", group: "" }, { label: "C", group: "" }], groups: [] };
const groups: GameContent = { ...order, mode: "groups", groups: ["X", "Y"], items: [{ label: "A", group: "X" }, { label: "B", group: "Y" }, { label: "C", group: "X" }, { label: "D", group: "Y" }] };

const shown = () => screen.getAllByRole("listitem").map((li) => within(li).getByTestId("sort-label").textContent);

describe("Game: sort it", () => {
  it("never starts in the right order", () => {
    render(<Game cell={cell("game", order)} session={session()} {...noop} />);
    expect(shown()).not.toEqual(["A", "B", "C"]);
  });
  it("wins only when the order is right", async () => {
    const action = vi.spyOn(api, "action").mockResolvedValue(undefined);
    render(<Game cell={cell("game", order)} session={session()} {...noop} />);
    await userEvent.click(screen.getByRole("button", { name: "Check" }));
    expect(action).not.toHaveBeenCalled();
    expect(screen.getByText(/not quite/i)).toBeInTheDocument();
    for (let guard = 0; guard < 6 && shown().join() !== "A,B,C"; guard++) {
      const list = shown();
      const i = list.findIndex((label, n) => label !== ["A", "B", "C"][n]);
      const j = list.indexOf(["A", "B", "C"][i]);
      await userEvent.click(screen.getByRole("button", { name: `Move ${list[j]} up` }));
    }
    await userEvent.click(screen.getByRole("button", { name: "Check" }));
    expect(action).toHaveBeenCalledWith("c1", "game.level_won", { level: 0, order: ["A", "B", "C"] });
  });
  it("sorts into groups by tapping", async () => {
    const action = vi.spyOn(api, "action").mockResolvedValue(undefined);
    render(<Game cell={cell("game", groups)} session={session()} {...noop} />);
    expect(screen.getByRole("button", { name: "Check" })).toBeDisabled();
    for (const it of groups.items) {
      await userEvent.click(screen.getByRole("button", { name: `Put ${it.label} in ${it.group}` }));
    }
    await userEvent.click(screen.getByRole("button", { name: "Check" }));
    expect(action).toHaveBeenCalledWith("c1", "game.level_won", { level: 0, placed: { A: "X", B: "Y", C: "X", D: "Y" } });
  });
  it("tells the server the student is mid-game, so the game isn't swapped", () => {
    const busy = vi.spyOn(api, "busy").mockResolvedValue(undefined);
    render(<Game cell={cell("game", target)} session={session()} {...noop} />);
    expect(busy).not.toHaveBeenCalled();
    fireEvent.change(screen.getByLabelText(/Push/), { target: { value: "20" } });
    expect(busy).toHaveBeenCalledWith("c1", true);
  });
  it("a new game with the same title starts clean instead of crashing", () => {
    const { rerender } = render(<Game cell={cell("game", { ...target, id: "g1" })} session={session()} {...noop} />);
    const next = { ...target, id: "g2", params: [{ id: "z", label: "Zoom", min: 0, max: 10, step: 1, value: 3, unit: "" }], output: { label: "Out", expr: "z*2", unit: "" }, levels: [{ target: 10, tolerance: 0.5, hint: "" }] };
    rerender(<Game cell={cell("game", next)} session={session()} {...noop} />);
    expect(screen.getByLabelText(/Zoom/)).toBeInTheDocument();
    expect(screen.getByTestId("game-value")).toHaveTextContent("6");
  });
});

describe("Journey", () => {
  const content = {
    topics: [
      { title: "What is a force?", milestone: "Force Finder", progress: 3, goal: 3, done: true },
      { title: "Friction", milestone: "Grip Expert", progress: 1, goal: 3, done: false },
      { title: "Gravity", milestone: "Gravity Guru", progress: 0, goal: 3, done: false },
    ],
    current: 1, complete: false, next: "2 steps to Grip Expert",
    shown: [{ concept: "Friction", tier: "moderate", why: "got quiz questions right" }],
  };
  it("shows the index, where the student is, and the next milestone", () => {
    render(<Journey cell={cell("journey", content)} session={session()} {...noop} />);
    expect(screen.getByText("2 steps to Grip Expert")).toBeInTheDocument();
    const here = screen.getByText("Friction", { selector: ".stop-title" }).closest("li")!;
    expect(here).toHaveAttribute("aria-current", "step");
    expect(screen.getByText("What is a force?").closest("li")).toHaveClass("stop-done");
    expect(screen.getByLabelText("1 of 3 steps")).toBeInTheDocument();
  });
  it("shows no percentages", () => {
    const { container } = render(<Journey cell={cell("journey", content)} session={session()} {...noop} />);
    expect(container.textContent).not.toContain("%");
  });
});

describe("Key Idea", () => {
  it("flips to show the example", async () => {
    render(<Concept cell={cell("concept", { title: "Force", idea: "A push or pull.", example: "Kicking a ball.", emoji: "⚡", related: ["Mass"] })} session={session()} {...noop} />);
    const card = screen.getByRole("button", { name: /Force/ });
    expect(card).toHaveAttribute("aria-pressed", "false");
    await userEvent.click(card);
    expect(card).toHaveAttribute("aria-pressed", "true");
    expect(screen.getByText("Kicking a ball.")).toBeInTheDocument();
  });
});

describe("What If", () => {
  it("has no reveal until the student guesses", () => {
    const { rerender } = render(<Thought cell={cell("thought", { scenario: "S", question: "Q" })} session={session()} {...noop} />);
    expect(screen.queryByText("R")).not.toBeInTheDocument();
    rerender(<Thought cell={cell("thought", { scenario: "S", question: "Q", reveal: "R", response: "mine", feedback: "F" })} session={session()} {...noop} />);
    expect(screen.getByText("R")).toBeInTheDocument();
  });
});

const object = (over: Partial<ConceptObject>): ConceptObject => ({
  id: "o1", pursuitId: "p1", type: "insight", content: "An idea", relatedIds: [], authorship: "ai", confidence: "low", status: "active", version: 1, updatedAt: "", ...over,
});

describe("My Notes", () => {
  it("marks who formed each idea", async () => {
    vi.spyOn(api, "listObjects").mockResolvedValue([object({ id: "a", content: "AI idea", authorship: "ai" }), object({ id: "b", content: "My idea", authorship: "learner" })]);
    render(<Document cell={cell("document", { objectCount: 2 })} session={session()} {...noop} />);
    const mine = await screen.findByText("My idea");
    const ai = await screen.findByText("AI idea");
    expect(within(mine.closest("li")!).getByText("You")).toBeInTheDocument();
    expect(within(ai.closest("li")!).getByText("AI")).toBeInTheDocument();
  });
  it("shows a conflict on a 409 save and keeps the student's text", async () => {
    vi.spyOn(api, "listObjects").mockResolvedValue([object({})]);
    vi.spyOn(api, "patchObject").mockRejectedValue(new ApiError(409, "version_conflict", "changed", { current: object({ content: "Newer", version: 2 }) }));
    render(<Document cell={cell("document", { objectCount: 1 })} session={session()} {...noop} />);
    await screen.findByText("An idea");
    await userEvent.click(screen.getByRole("button", { name: /edit/i }));
    const box = screen.getByRole("textbox", { name: /edit/i });
    await userEvent.clear(box);
    await userEvent.type(box, "My careful rewrite");
    await userEvent.click(screen.getByRole("button", { name: /save/i }));
    await screen.findByText(/changed elsewhere/i);
    expect(screen.getByRole("textbox", { name: /edit/i })).toHaveValue("My careful rewrite");
    expect(screen.getByText(/Newer/)).toBeInTheDocument();
  });
  it("keeps half-typed text and flags a conflict when another tab saves", async () => {
    const list = vi.spyOn(api, "listObjects").mockResolvedValue([object({})]);
    const view = render(<Document cell={cell("document", { objectCount: 1 })} session={session({ documentRev: 0 })} {...noop} />);
    await screen.findByText("An idea");
    await userEvent.click(screen.getByRole("button", { name: /edit/i }));
    const box = screen.getByRole("textbox", { name: /edit/i });
    await userEvent.clear(box);
    await userEvent.type(box, "My careful rewrite");
    list.mockResolvedValue([object({ content: "From the other tab", version: 2 })]);
    view.rerender(<Document cell={cell("document", { objectCount: 1 })} session={session({ documentRev: 1 })} {...noop} />);
    await screen.findByText(/changed elsewhere/i);
    expect(screen.getByRole("textbox", { name: /edit/i })).toHaveValue("My careful rewrite");
    expect(screen.getByText(/From the other tab/)).toBeInTheDocument();
  });
});

const msg = (over: Partial<Message>): Message => ({ id: "m", sessionId: "s", role: "assistant", content: "hi", interrupted: false, createdAt: "", ...over });

describe("Chat", () => {
  it("keeps the text in the input when sending fails", async () => {
    vi.spyOn(api, "action").mockRejectedValue(new ApiError(0, "network", "Can't reach DeepFlow."));
    render(<Dialogue cell={cell("dialogue", {})} session={session()} {...noop} />);
    const box = screen.getByRole("textbox", { name: /message/i });
    await userEvent.type(box, "my long thought{Enter}");
    await screen.findByText(/Can't reach/);
    expect(box).toHaveValue("my long thought");
  });
  it("clears the input after a successful send", async () => {
    const action = vi.spyOn(api, "action").mockResolvedValue(undefined);
    render(<Dialogue cell={cell("dialogue", {})} session={session()} {...noop} />);
    const box = screen.getByRole("textbox", { name: /message/i });
    await userEvent.type(box, "hello{Enter}");
    await waitFor(() => expect(box).toHaveValue(""));
    expect(action).toHaveBeenCalledWith("c1", "dialogue.user_message", { text: "hello" });
  });
  it("labels an interrupted message", () => {
    render(<Dialogue cell={cell("dialogue", {})} session={session({ messages: [msg({ interrupted: true, content: "partial" })] })} {...noop} />);
    expect(screen.getByText(/interrupted/i)).toBeInTheDocument();
  });
  it("renders model text as text, not HTML", () => {
    const { container } = render(<Dialogue cell={cell("dialogue", {})} session={session({ messages: [msg({ content: "<img src=x onerror=alert(1)> **bold**" })] })} {...noop} />);
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("strong")?.textContent).toBe("bold");
  });
});

describe("useDraft", () => {
  it("adopts a restored draft when the box is empty, but never overwrites typing", () => {
    const { result, rerender } = renderHook(({ init }) => useDraft("c1", init), { initialProps: { init: "" } });
    rerender({ init: "restored" });
    expect(result.current.text).toBe("restored");
    act(() => result.current.setText("newer typing"));
    rerender({ init: "older server copy" });
    expect(result.current.text).toBe("newer typing");
  });
});

describe("useBusy", () => {
  it("stops renewing when inactive", () => {
    vi.useFakeTimers();
    const busy = vi.spyOn(api, "busy").mockResolvedValue(undefined);
    const { rerender } = renderHook(({ on }) => useBusy("c1", on), { initialProps: { on: true } });
    expect(busy).toHaveBeenCalledWith("c1", true);
    act(() => { vi.advanceTimersByTime(10_000); });
    expect(busy).toHaveBeenCalledTimes(2);
    rerender({ on: false });
    expect(busy).toHaveBeenLastCalledWith("c1", false);
    const n = busy.mock.calls.length;
    act(() => { vi.advanceTimersByTime(60_000); });
    expect(busy.mock.calls.length).toBe(n);
  });
});
