import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "./api";
import type { Cell, Decided, Message, Milestone, RetrievalQuestion, SessionState, Workspace } from "./types";

export interface ServerEvent { seq: number; name: string; data: unknown }

export const EVENT_NAMES = [
  "cell.updated", "cell.delta", "message.done", "layout.changed",
  "document.changed", "milestone.reached", "decision.made", "retrieval.ready",
];

export function initialState(): SessionState {
  return {
    loaded: false, messages: [], drafts: {}, artifacts: [], retrieval: [], decisions: [],
    live: false, seq: 0, needsResync: false, documentRev: 0, milestoneCount: 0,
  };
}

// reduce applies one server event. It is pure, and is the only way workspace
// state changes on the client: the renderer makes no decisions of its own.
export function reduce(s: SessionState, ev: ServerEvent): SessionState {
  if (ev.seq <= s.seq) return s;
  const next: SessionState = { ...s, seq: ev.seq };
  if (ev.seq > s.seq + 1) next.needsResync = true;
  switch (ev.name) {
    case "cell.updated": {
      const cell = ev.data as Cell;
      if (s.workspace) {
        next.workspace = { ...s.workspace, cells: s.workspace.cells.map((c) => (c.id === cell.id ? cell : c)) };
      }
      break;
    }
    case "layout.changed":
      next.workspace = ev.data as Workspace;
      break;
    case "cell.delta": {
      const d = ev.data as { messageId: string; text: string };
      const existing = s.messages.find((m) => m.id === d.messageId);
      next.messages = existing
        ? s.messages.map((m) => (m.id === d.messageId ? { ...m, content: m.content + d.text, streaming: true } : m))
        : [...s.messages, { id: d.messageId, sessionId: s.session?.id ?? "", role: "assistant", content: d.text, interrupted: false, createdAt: "", streaming: true }];
      break;
    }
    case "message.done": {
      const m = ev.data as Message;
      next.messages = s.messages.some((x) => x.id === m.id)
        ? s.messages.map((x) => (x.id === m.id ? { ...m, streaming: false } : x))
        : m.content ? [...s.messages, m] : s.messages;
      break;
    }
    case "document.changed":
      next.documentRev = s.documentRev + 1;
      break;
    case "milestone.reached":
      next.milestone = ev.data as Milestone;
      next.milestoneCount = s.milestoneCount + 1;
      break;
    case "decision.made":
      next.decisions = [...s.decisions, ev.data as Decided];
      break;
    case "retrieval.ready":
      next.retrieval = ev.data as RetrievalQuestion[];
      break;
  }
  return next;
}

export interface UseSession {
  state: SessionState;
  connected: boolean;
  reload: () => Promise<void>;
  setRetrieval: (r: RetrievalQuestion[]) => void;
}

export function useSession(sessionId: string): UseSession {
  const [state, setState] = useState<SessionState>(initialState);
  const [connected, setConnected] = useState(false);
  const [streamKey, setStreamKey] = useState(0);
  const seqRef = useRef(0);

  const reload = useCallback(async () => {
    try {
      const loaded = await api.getSession(sessionId);
      seqRef.current = loaded.seq;
      setState((s) => ({ ...loaded, loaded: true, needsResync: false, documentRev: s.documentRev + 1, milestone: s.milestone, milestoneCount: s.milestoneCount }));
    } catch (e) {
      setState((s) => ({ ...s, loaded: true, error: e instanceof Error ? e.message : String(e) }));
    }
  }, [sessionId]);

  useEffect(() => {
    let cancelled = false;
    reload().then(() => { if (!cancelled) setStreamKey((k) => k + 1); });
    return () => { cancelled = true; };
  }, [reload]);

  useEffect(() => {
    if (streamKey === 0 || !state.live) return;
    const es = new EventSource(`/api/sessions/${sessionId}/stream?after=${seqRef.current}`);
    let opened = false;
    es.onopen = () => {
      setConnected(true);
      // After a dropped connection, the server may have restarted: start from truth.
      if (opened) void reload();
      opened = true;
    };
    es.onerror = () => setConnected(false);
    for (const name of EVENT_NAMES) {
      es.addEventListener(name, (e) => {
        const m = e as MessageEvent<string>;
        const ev: ServerEvent = { seq: Number(m.lastEventId), name, data: JSON.parse(m.data) };
        setState((s) => reduce(s, ev));
      });
    }
    es.addEventListener("resync", () => void reload());
    return () => es.close();
    // Reconnecting is EventSource's job; this runs once per loaded session.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [streamKey, sessionId, state.live]);

  useEffect(() => {
    if (state.needsResync) void reload();
  }, [state.needsResync, reload]);

  const setRetrieval = useCallback((r: RetrievalQuestion[]) => setState((s) => ({ ...s, retrieval: r })), []);

  return { state, connected, reload, setRetrieval };
}
