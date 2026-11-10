import { useState } from "react";
import type { Artifact, Decided } from "../types";

const LABELS: Record<string, string> = {
  REPLACE: "Swapped a card", RETIRE: "Removed a card", SPAWN: "Added a card", UPDATE: "Updated a card",
  DEEPEN: "Made it harder", SIMPLIFY: "Made it simpler", KEEP: "Kept things as they were",
};

interface Props { decisions: Decided[]; artifacts: Artifact[]; onClose: () => void; onUndo: () => Promise<void> }

export function ActivityPanel({ decisions, artifacts, onClose, onUndo }: Props) {
  const [note, setNote] = useState("");
  const undo = () => {
    setNote("");
    onUndo().catch(() => setNote("There's nothing left to undo."));
  };
  return (
    <aside className="activity" aria-label="Why the workspace changed">
      <header className="row"><h2>What changed</h2><span className="grow" /><button onClick={onClose}>Close</button></header>
      {decisions.length === 0 && <p className="waiting">Nothing yet.</p>}
      {decisions.some((d) => d.canUndo) && <button onClick={undo}>Undo the last change</button>}
      {note && <p className="note" role="status">{note}</p>}
      <ol>
        {[...decisions].reverse().map((d, i) => (
          <li key={i}>
            <b>{LABELS[d.decision.action] ?? d.decision.action}</b>
            {d.decision.reason && <p>{d.decision.reason}</p>}
                      </li>
        ))}
      </ol>
      <h3>Kept for you</h3>
      {artifacts.length === 0 ? <p className="note">Nothing yet.</p> : (
        <ul>
          {artifacts.map((a) => (
            <li key={a.id}>
              <b>{a.cellType}</b>{a.learnerAuthored ? " · includes your writing" : ""}
              <details><summary>View</summary><pre>{JSON.stringify(a.content, null, 2)}</pre></details>
            </li>
          ))}
        </ul>
      )}
    </aside>
  );
}
