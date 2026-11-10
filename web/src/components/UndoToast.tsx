import { useEffect, useState } from "react";
import type { Decided } from "../types";

const VISIBLE_MS = 12_000;

export function UndoToast({ decisions, onUndo }: { decisions: Decided[]; onUndo: () => Promise<void> }) {
  const undoable = decisions.filter((d) => d.canUndo);
  const latest = undoable[undoable.length - 1];
  const count = undoable.length;
  const [dismissed, setDismissed] = useState(count);

  useEffect(() => {
    if (count === dismissed) return;
    const t = setTimeout(() => setDismissed(count), VISIBLE_MS);
    return () => clearTimeout(t);
  }, [count, dismissed]);

  // Shown again only when a newer change arrives.
  const show = latest && count > dismissed;
  return (
    <div className="toast-region" role="status" aria-live="polite">
      {show && (
        <div className="toast">
          <span>{latest.decision.reason || "The workspace changed."}</span>
          <button onClick={() => { setDismissed(count); void onUndo().catch(() => {}); }}>Undo</button>
          <button aria-label="Dismiss" onClick={() => setDismissed(count)}>✕</button>
        </div>
      )}
    </div>
  );
}
