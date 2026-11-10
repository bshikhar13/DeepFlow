export type CellType = "dialogue" | "concept" | "game" | "quiz" | "journey" | "thought" | "document" | "map";

export type CellStatus =
  | "proposed" | "initializing" | "active" | "waiting" | "updating"
  | "suspended" | "retiring" | "terminated" | "failed";

export interface Layout { col: number; row: number; colSpan: number; rowSpan: number }

export interface Cell {
  id: string;
  type: CellType;
  agentInstanceId: string;
  objective: string;
  status: CellStatus;
  pinned: boolean;
  busy: boolean;
  layout: Layout;
  content?: unknown;
  contextVersion: number;
  userRevision: number;
  error?: string;
}

export interface Decision {
  action: string;
  cellId?: string;
  cellType?: CellType;
  objective?: string;
  reason?: string;
  unsolicited: boolean;
}

export interface Decided { decision: Decision; canUndo: boolean; at: string }

export interface Workspace { sessionId: string; cells: Cell[]; revision: number; pending?: Decision[] }

export interface Message {
  id: string;
  sessionId: string;
  role: "user" | "assistant";
  content: string;
  interrupted: boolean;
  createdAt: string;
  streaming?: boolean;
}

export interface Pursuit { id: string; title: string; description: string; grade: number; createdAt: string; updatedAt: string }

export interface Session {
  id: string;
  pursuitId: string;
  inquiry: string;
  startedAt: string;
  endedAt: string;
  summary: string;
  needsConsolidation: boolean;
}

export interface Artifact { id: string; cellId: string; cellType: CellType; content: unknown; learnerAuthored: boolean; createdAt: string }

export interface RetrievalQuestion { id: string; prompt: string; concept: string; status: "open" | "answered" | "skipped" }

export type ObjectType = "concept" | "hypothesis" | "insight" | "connection" | "question";
export type Authorship = "ai" | "learner" | "edited" | "reference";

export interface ConceptObject {
  id: string;
  pursuitId: string;
  type: ObjectType;
  content: string;
  relatedIds: string[];
  authorship: Authorship;
  confidence: string;
  status: string;
  sourceEventId?: string;
  version: number;
  updatedAt: string;
}

export interface Evidence {
  id: string;
  concept: string;
  learnerAction: string;
  tier: "weak" | "moderate" | "strong" | "durable";
  correctness: string;
  reasoning: string;
  rationale: string;
  uncertainty: string;
  sourceEventId: string;
  createdAt: string;
}

export interface GameParam { id: string; label: string; min: number; max: number; step: number; value: number; unit: string }

export interface GameContent {
  id?: string;
  kind: "target" | "sort" | "locate" | string;
  title: string;
  goal: string;
  params: GameParam[];
  output: { label: string; expr: string; unit: string };
  levels: { target: number; tolerance: number; hint: string }[];
  note: string;
  basis: string;
  mode: string;
  items: { label: string; group: string }[];
  groups: string[];
  places?: MapPlace[];
  toleranceKm?: number;
  won: number[];
  stars: number;
}

export interface MapPlace { label: string; lat: number; lon: number; fact: string; when: string }

export interface MapContent { title: string; places: MapPlace[]; connect: boolean }

export interface QuizContent {
  id: string;
  prompt: string;
  options: string[];
  topic: string;
  level: number;
  streak: number;
  right: number;
  result?: { correct: boolean; chosen: number; answer: number; why: string };
}

export interface JourneyContent {
  topics: { title: string; milestone: string; progress: number; goal: number; done: boolean }[];
  current: number;
  complete: boolean;
  next: string;
  shown: { concept: string; tier: string; why: string }[];
}

export interface ConceptContent { title: string; idea: string; example: string; emoji: string; related: string[] }

export interface ThoughtContent {
  scenario: string; question: string;
  reveal?: string; feedback?: string; response?: string;
}

export interface Milestone { title: string; milestone: string; next: string; complete: boolean }

export interface SessionState {
  loaded: boolean;
  error?: string;
  session?: Session;
  pursuit?: Pursuit;
  workspace?: Workspace;
  messages: Message[];
  drafts: Record<string, string>;
  artifacts: Artifact[];
  retrieval: RetrievalQuestion[];
  decisions: Decided[];
  live: boolean;
  seq: number;
  needsResync: boolean;
  documentRev: number;
  milestone?: Milestone;
  milestoneCount: number;
}

export interface Status { provider: string; vendor: string; model: string; consentRequired: boolean }
