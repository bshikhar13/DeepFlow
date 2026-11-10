import type { ConceptObject, Evidence, Pursuit, Session, SessionState, Status } from "./types";

export class ApiError extends Error {
  constructor(public status: number, public code: string, message: string, public body?: unknown) {
    super(message);
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  let res: Response;
  try {
    res = await fetch(path, {
      method,
      headers: body === undefined ? undefined : { "Content-Type": "application/json" },
      body: body === undefined ? undefined : JSON.stringify(body),
    });
  } catch {
    throw new ApiError(0, "network", "Can't reach DeepFlow. Is the server still running?");
  }
  if (res.status === 204 || res.status === 202) return undefined as T;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) {
    throw new ApiError(res.status, data?.error?.code ?? "error", data?.error?.message ?? "Request failed.", data);
  }
  return data as T;
}

function normalize(raw: Record<string, unknown>): SessionState {
  return {
    loaded: true, messages: [], drafts: {}, artifacts: [], retrieval: [], decisions: [],
    live: false, seq: 0, needsResync: false, documentRev: 0, milestoneCount: 0,
    ...raw,
  } as SessionState;
}

export const api = {
  status: () => request<Status>("GET", "/api/status"),
  consent: () => request<void>("POST", "/api/consent"),
  listPursuits: () => request<Pursuit[]>("GET", "/api/pursuits"),
  createPursuit: (title: string, inquiry: string, grade: number) => request<Pursuit>("POST", "/api/pursuits", { title, inquiry, grade }),
  getPursuit: (id: string) => request<{ pursuit: Pursuit; sessions: Session[] }>("GET", `/api/pursuits/${id}`),
  deletePursuit: (id: string) => request<void>("DELETE", `/api/pursuits/${id}`),
  startSession: async (pursuitId: string, inquiry: string) =>
    normalize(await request<Record<string, unknown>>("POST", `/api/pursuits/${pursuitId}/sessions`, { inquiry })),
  getSession: async (id: string) => normalize(await request<Record<string, unknown>>("GET", `/api/sessions/${id}`)),
  endSession: (id: string) => request<Session>("POST", `/api/sessions/${id}/end`),
  undo: (id: string) => request<void>("POST", `/api/sessions/${id}/undo`),
  reassess: (id: string) => request<void>("POST", `/api/sessions/${id}/reassess`),
  answerRetrieval: (sessionId: string, qid: string, answer: string, skip: boolean) =>
    request<void>("POST", `/api/sessions/${sessionId}/retrieval/${qid}`, { answer, skip }),
  action: (cellId: string, type: string, payload: unknown = {}) =>
    request<void>("POST", `/api/cells/${cellId}/action`, { type, payload, client_id: crypto.randomUUID() }),
  busy: (cellId: string, busy: boolean) => request<void>("POST", `/api/cells/${cellId}/busy`, { busy }),
  draft: (cellId: string, content: string) => request<void>("PUT", `/api/cells/${cellId}/draft`, { content }),
  control: (cellId: string, op: "pin" | "unpin" | "remove" | "refresh" | "retry" | "stop") =>
    request<void>("POST", `/api/cells/${cellId}/${op}`),
  listObjects: (pursuitId: string) => request<ConceptObject[]>("GET", `/api/pursuits/${pursuitId}/objects`),
  listEvidence: (pursuitId: string) => request<Evidence[]>("GET", `/api/pursuits/${pursuitId}/evidence`),
  patchObject: (id: string, content: string, version: number) =>
    request<ConceptObject>("PATCH", `/api/objects/${id}`, { content, version }),
  challengeObject: (id: string, text: string) => request<void>("POST", `/api/objects/${id}/challenge`, { text }),
  objectEvidence: (id: string) => request<Evidence[]>("GET", `/api/objects/${id}/evidence`),
};
