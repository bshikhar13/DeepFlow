import { useState, type ReactNode } from "react";
import { api } from "../api";
import { errorText } from "../cells/common";
import type { Cell } from "../types";
import { CardBoundary } from "./CardBoundary";

const icon = (d: string) => (
  <svg viewBox="0 0 16 16" width="14" height="14" fill="none" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true"><path d={d} /></svg>
);
const ICONS = {
  expand: icon("M6 2H2v4M10 2h4v4M6 14H2v-4M10 14h4v-4"),
  collapse: icon("M2 6h4V2M14 6h-4V2M2 10h4v4M14 10h-4v4"),
  pin: icon("M6 2h4l-.5 5 2.5 2.5H4L6.5 7 6 2zM8 9.5V14"),
  refresh: icon("M13.5 8a5.5 5.5 0 1 1-1.6-3.9M13.5 2v3h-3"),
  remove: icon("M4 4l8 8M12 4l-8 8"),
  more: icon("M3 8h.01M8 8h.01M13 8h.01"),
};

const STATUS: Partial<Record<Cell["status"], string>> = {
  suspended: "Paused", failed: "Oops",
};

interface Props { cell: Cell; title: string; icon: string; focused: boolean; hidden: boolean; sessionId: string; onExpand: (el: HTMLElement) => void; children: ReactNode }

export function CellFrame({ cell, title, icon, focused, hidden, sessionId, onExpand, children }: Props) {
  const [error, setError] = useState("");
  const op = (name: "pin" | "unpin" | "refresh" | "remove" | "retry") => {
    setError("");
    api.control(cell.id, name).catch((e) => setError(errorText(e)));
  };
  const { col, row, colSpan, rowSpan } = cell.layout;
  const style = focused ? undefined : { gridColumn: `${col} / span ${colSpan}`, gridRow: `${row} / span ${rowSpan}` };
  const label = STATUS[cell.status];
  const preparing = cell.status === "initializing" && !cell.content;
  return (
    <section
      className={`cell cell-${cell.type} ${focused ? "cell-focused" : ""} ${cell.pinned ? "cell-pinned" : ""}`}
      style={hidden ? { ...style, display: "none" } : style}
      aria-label={title}
    >
      <header className="cell-head">
        <h2><span aria-hidden="true">{icon}</span> {title}</h2>
        {label && <span className={`status status-${cell.status}`} role="status">{label}</span>}
        <div className="cell-controls">
          <button aria-label={focused ? `Exit focus on ${title}` : `Expand ${title}`} title={focused ? "Exit focus (Esc)" : "Expand"} onClick={(e) => onExpand(e.currentTarget)}>{focused ? ICONS.collapse : ICONS.expand}</button>
          <button aria-label={cell.pinned ? `Unpin ${title}` : `Pin ${title}`} aria-pressed={cell.pinned} title={cell.pinned ? "Pinned" : "Pin"} onClick={() => op(cell.pinned ? "unpin" : "pin")}>{ICONS.pin}</button>
          {cell.type !== "dialogue" && cell.type !== "document" && cell.type !== "journey" && (
            <button aria-label={`Refresh ${title}`} title="Try something else" onClick={() => op("refresh")}>{ICONS.refresh}</button>
          )}
          {cell.type !== "dialogue" && <button aria-label={`Remove ${title}`} title="Remove" onClick={() => op("remove")}>{ICONS.remove}</button>}
          <details className="more">
            <summary aria-label={`More about ${title}`} title="More">{ICONS.more}</summary>
            <div className="more-menu">
              <p><b>Purpose:</b> {cell.objective}</p>
              <p className="note">Agent {cell.agentInstanceId.slice(-6)} · {cell.status}</p>
              <button onClick={() => void api.reassess(sessionId)}>Reassess workspace</button>
            </div>
          </details>
        </div>
      </header>
      {cell.status === "failed" && (
        <div className="cell-failed" role="alert">
          <span>{cell.error || "Something went wrong."}</span>
          <button onClick={() => op("retry")}>Retry</button>
        </div>
      )}
      {cell.status !== "failed" && cell.error && <p className="note cell-note">{cell.error}</p>}
      {error && <p className="error cell-note" role="alert">{error}</p>}
      <div className={cell.status === "updating" ? "cell-body cell-busy" : "cell-body"}>
        {preparing ? <div className="skeleton" role="status" aria-label="Getting this ready"><span /><span /><span /></div> : <CardBoundary>{children}</CardBoundary>}
      </div>
    </section>
  );
}
