import { describe, expect, it } from "vitest";
import { initialState, reduce } from "./state";
import type { Cell, SessionState } from "./types";

const cell = (id: string, extra: Partial<Cell> = {}): Cell => ({
  id, type: "concept", agentInstanceId: "a", objective: "o", status: "waiting", pinned: false, busy: false,
  layout: { col: 1, row: 1, colSpan: 3, rowSpan: 1 }, contextVersion: 0, userRevision: 0, ...extra,
});

function base(): SessionState {
  const s = initialState();
  s.seq = 10;
  s.workspace = { sessionId: "s", cells: [cell("a"), cell("b")], revision: 1 };
  s.drafts = { a: "my draft" };
  return s;
}

describe("reduce", () => {
  it("applies deltas only to the matching message", () => {
    let s = reduce(base(), { seq: 11, name: "cell.delta", data: { cellId: "d", messageId: "m1", text: "he" } });
    s = reduce(s, { seq: 12, name: "cell.delta", data: { cellId: "d", messageId: "m2", text: "x" } });
    s = reduce(s, { seq: 13, name: "cell.delta", data: { cellId: "d", messageId: "m1", text: "llo" } });
    expect(s.messages.find((m) => m.id === "m1")?.content).toBe("hello");
    expect(s.messages.find((m) => m.id === "m2")?.content).toBe("x");
  });
  it("replaces a streaming message with its final form", () => {
    let s = reduce(base(), { seq: 11, name: "cell.delta", data: { cellId: "d", messageId: "m1", text: "par" } });
    s = reduce(s, { seq: 12, name: "message.done", data: { id: "m1", sessionId: "s", role: "assistant", content: "partial", interrupted: true, createdAt: "" } });
    const m = s.messages.find((x) => x.id === "m1")!;
    expect(m.content).toBe("partial");
    expect(m.interrupted).toBe(true);
    expect(m.streaming).toBeFalsy();
  });
  it("removes cells absent from a layout change", () => {
    const s = reduce(base(), { seq: 11, name: "layout.changed", data: { sessionId: "s", cells: [cell("b")], revision: 2 } });
    expect(s.workspace?.cells.map((c) => c.id)).toEqual(["b"]);
  });
  it("flags a sequence gap for resync", () => {
    const s = reduce(base(), { seq: 15, name: "document.changed", data: {} });
    expect(s.needsResync).toBe(true);
  });
  it("ignores events it has already seen", () => {
    const b = base();
    const s = reduce(b, { seq: 9, name: "layout.changed", data: { sessionId: "s", cells: [], revision: 2 } });
    expect(s.workspace?.cells).toHaveLength(2);
  });
  it("cell updates leave the local draft alone", () => {
    const s = reduce(base(), { seq: 11, name: "cell.updated", data: cell("a", { userRevision: 3, content: { title: "new" } }) });
    expect(s.drafts.a).toBe("my draft");
    expect(s.workspace?.cells[0].userRevision).toBe(3);
  });
  it("counts document changes", () => {
    const s = reduce(base(), { seq: 11, name: "document.changed", data: {} });
    expect(s.documentRev).toBe(1);
  });
  it("remembers the latest milestone for the celebration", () => {
    const s = reduce(base(), { seq: 11, name: "milestone.reached", data: { title: "Force", milestone: "Force Finder", next: "Friction", complete: false } });
    expect(s.milestone?.milestone).toBe("Force Finder");
    expect(s.milestoneCount).toBe(1);
  });
  it("logs decisions for the activity panel", () => {
    const s = reduce(base(), { seq: 11, name: "decision.made", data: { decision: { action: "REPLACE", reason: "moved on", unsolicited: true }, canUndo: true, at: "" } });
    expect(s.decisions[0].decision.reason).toBe("moved on");
  });
});
