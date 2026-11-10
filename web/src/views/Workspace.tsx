import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "../api";
import { CELL_ICONS, CELL_TITLES, CellRegistry } from "../cells/registry";
import { ActivityPanel } from "../components/ActivityPanel";
import { CellFrame } from "../components/CellFrame";
import { Header } from "../components/Header";
import { MilestoneBanner } from "../components/MilestoneBanner";
import { UndoToast } from "../components/UndoToast";
import type { Prefs } from "../prefs";
import { useSession } from "../state";
import type { Status } from "../types";
import { Retrieval } from "./Retrieval";

interface Props { sessionId: string; status: Status; prefs: Prefs; onExit: (notice?: string) => void }

export function WorkspaceView({ sessionId, status, prefs, onExit }: Props) {
  const { state, connected, reload, setRetrieval } = useSession(sessionId);
  // Undo can restore a draft, which isn't part of the event stream.
  const undo = useCallback(() => api.undo(sessionId).then(reload), [sessionId, reload]);
  const [focused, setFocused] = useState<string | null>(null);
  const [activity, setActivity] = useState(false);
  const [prefill, setPrefill] = useState("");
  const [ending, setEnding] = useState(false);
  const opener = useRef<HTMLElement | null>(null);
  const clearPrefill = useCallback(() => setPrefill(""), []);
  const discuss = useCallback((text: string) => {
    setFocused(null);
    setPrefill(text);
  }, []);

  useEffect(() => {
    const on = (e: KeyboardEvent) => {
      if (e.key === "Escape" && focused) {
        setFocused(null);
        opener.current?.focus();
      }
    };
    addEventListener("keydown", on);
    return () => removeEventListener("keydown", on);
  }, [focused]);

  useEffect(() => {
    if (state.loaded && !state.error && !state.live) onExit("That session ended. Pick it up below.");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [state.loaded, state.live, state.error]);

  if (!state.loaded) return <main className="center"><p className="waiting">Opening your workspace…</p></main>;
  if (state.error) {
    return (
      <main className="center">
        <p className="error" role="alert">{state.error}</p>
        <button onClick={() => onExit()}>Back to pursuits</button>
      </main>
    );
  }

  const cells = state.workspace?.cells ?? [];
  const rows = Math.max(1, ...cells.map((c) => c.layout.row + c.layout.rowSpan - 1));
  const focusedExists = cells.some((c) => c.id === focused);

  const end = async () => {
    setEnding(true);
    try {
      const s = await api.endSession(sessionId);
      onExit(s.summary ? `Saved! ${s.summary}` : "Saved!");
    } catch {
      setEnding(false);
      onExit("Couldn't reach the server, but your work is saved.");
    }
  };

  return (
    <div className="app">
      <Header
        title={state.pursuit?.title ?? ""} inquiry={state.session?.inquiry ?? ""} status={status} prefs={prefs}
        connected={connected} ending={ending} onActivity={() => setActivity(!activity)} activityOpen={activity}
        onHome={() => onExit()} onEnd={() => void end()}
      />
      <Retrieval sessionId={sessionId} questions={state.retrieval} onChange={setRetrieval} />
      <main
        className={`grid ${focused && focusedExists ? "grid-focus" : ""}`}
        style={{ gridTemplateRows: focused && focusedExists ? undefined : `repeat(${rows}, minmax(280px, 1fr))` }}
      >
        {cells.map((c) => {
          const Body = CellRegistry[c.type];
          const isFocused = focused === c.id;
          return (
            <CellFrame
              key={c.id} cell={c} title={CELL_TITLES[c.type]} icon={CELL_ICONS[c.type]}
              focused={isFocused} hidden={!!focused && focusedExists && !isFocused} sessionId={sessionId}
              onExpand={(el) => {
                opener.current = el;
                setFocused(isFocused ? null : c.id);
              }}
            >
              <Body cell={c} session={state} prefill={c.type === "dialogue" ? prefill : ""} clearPrefill={clearPrefill} discuss={discuss} />
            </CellFrame>
          );
        })}
      </main>
      {activity && <ActivityPanel decisions={state.decisions} artifacts={state.artifacts} onClose={() => setActivity(false)} onUndo={undo} />}
      <MilestoneBanner milestone={state.milestone} count={state.milestoneCount} />
      <UndoToast decisions={state.decisions} onUndo={undo} />
    </div>
  );
}
