import { useState } from "react";
import { api } from "../api";
import type { ThoughtContent } from "../types";
import { Rich, useSubmit, type CellProps } from "./common";
import { useBusy } from "./useBusy";
import { useDraft } from "./useDraft";

export function Thought({ cell, session }: CellProps) {
  const c = cell.content as ThoughtContent | undefined;
  const { text, setText, clear } = useDraft(cell.id, session.drafts[cell.id] ?? "");
  const [editing, setEditing] = useState(false);
  useBusy(cell.id, editing || text.length > 0);
  const { error, sending, submit } = useSubmit((t) => api.action(cell.id, "thought.response_submitted", { text: t }), clear);
  if (!c?.scenario) return null;
  return (
    <div className="thought">
      <p className="quiz-q"><Rich text={c.scenario} /></p>
      <p><Rich text={c.question} /></p>
      {c.reveal ? (
        <div className="quiz-why quiz-why-right" role="status">
          {c.response && <p className="note">You said: {c.response}</p>}
          <p><b><Rich text={c.reveal} /></b></p>
          {c.feedback && <p><Rich text={c.feedback} /></p>}
        </div>
      ) : session.live && (
        <form onSubmit={(e) => { e.preventDefault(); void submit(text); }}>
          <label className="field">
            <span className="sr-only">Your guess</span>
            <textarea rows={2} placeholder="Your guess…" value={text} onChange={(e) => setText(e.target.value)} onFocus={() => setEditing(true)} onBlur={() => setEditing(false)} />
          </label>
          <div className="row"><button type="submit" className="primary" disabled={!text.trim() || sending || cell.status === "updating"}>Lock it in</button></div>
          {error && <p className="error" role="alert">{error}</p>}
        </form>
      )}
    </div>
  );
}
