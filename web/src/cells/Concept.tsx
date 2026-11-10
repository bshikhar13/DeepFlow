import { useState } from "react";
import { api } from "../api";
import type { ConceptContent } from "../types";
import { Rich, type CellProps } from "./common";

export function Concept({ cell }: CellProps) {
  const c = cell.content as ConceptContent | undefined;
  const [flipped, setFlipped] = useState(false);
  if (!c?.title) return null;
  return (
    <div className="concept">
      <button className={`flip ${flipped ? "flip-on" : ""}`} aria-pressed={flipped} onClick={() => setFlipped(!flipped)}>
        <span className="flip-inner">
          <span className="flip-face flip-front" aria-hidden={flipped}>
            <span className="flip-emoji" aria-hidden="true">{c.emoji}</span>
            <span className="flip-title"><Rich text={c.title} /></span>
            <span className="flip-text"><Rich text={c.idea} /></span>
          </span>
          <span className="flip-face flip-back" aria-hidden={!flipped}>
            <span className="flip-label">For example</span>
            <span className="flip-text"><Rich text={c.example} /></span>
          </span>
        </span>
      </button>
      {c.related?.length > 0 && (
        <div className="chips">
          {c.related.map((x) => <button key={x} className="chip" onClick={() => void api.action(cell.id, "concept.explored", { concept: x }).catch(() => {})}><Rich text={x} /></button>)}
        </div>
      )}
    </div>
  );
}
