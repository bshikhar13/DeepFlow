import { useState } from "react";
import { api } from "../api";
import { Rich, errorText } from "../cells/common";
import type { RetrievalQuestion } from "../types";

export function Retrieval({ sessionId, questions, onChange }: { sessionId: string; questions: RetrievalQuestion[]; onChange: (q: RetrievalQuestion[]) => void }) {
  const current = questions.find((q) => q.status === "open");
  const [text, setText] = useState("");
  const [error, setError] = useState("");
  if (!current) return null;
  const n = questions.findIndex((q) => q.id === current.id) + 1;
  const respond = async (skip: boolean) => {
    setError("");
    try {
      await api.answerRetrieval(sessionId, current.id, text, skip);
      onChange(questions.map((q) => (q.id === current.id ? { ...q, status: skip ? "skipped" : "answered" } : q)));
      setText("");
    } catch (e) { setError(errorText(e)); }
  };
  return (
    <section className="retrieval" aria-label="Retrieval from your last session">
      <div>
        <span className="eyebrow">🧠 From memory · {n} of {questions.length}</span>
        <p className="retrieval-q"><Rich text={current.prompt} /></p>
      </div>
      <div className="retrieval-a">
        <textarea rows={2} aria-label="Your answer" value={text} onChange={(e) => setText(e.target.value)} />
        <div className="row">
          <button className="primary" disabled={!text.trim()} onClick={() => void respond(false)}>Answer</button>
          <button onClick={() => void respond(true)}>Skip</button>
        </div>
        {error && <p className="error" role="alert">{error}</p>}
      </div>
    </section>
  );
}
