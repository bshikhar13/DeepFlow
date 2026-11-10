import { useEffect, useRef, useState } from "react";
import { api } from "../api";
import { Prose, useSubmit, type CellProps } from "./common";
import { useBusy } from "./useBusy";
import { useDraft } from "./useDraft";

export function Dialogue({ cell, session, prefill, clearPrefill }: CellProps) {
  const { text, setText, clear } = useDraft(cell.id, session.drafts[cell.id] ?? "");
  const [focused, setFocused] = useState(false);
  useBusy(cell.id, focused || text.length > 0);
  const { error, sending, submit } = useSubmit(
    (t) => api.action(cell.id, "dialogue.user_message", { text: t }),
    clear,
  );
  const box = useRef<HTMLTextAreaElement>(null);
  const end = useRef<HTMLDivElement>(null);
  const streaming = session.messages.some((m) => m.streaming) || cell.status === "updating";
  const last = session.messages[session.messages.length - 1];

  useEffect(() => { end.current?.scrollIntoView?.({ block: "end" }); }, [session.messages.length, last?.content]);

  useEffect(() => {
    if (prefill) {
      setText(prefill);
      clearPrefill();
      box.current?.focus();
    }
  }, [prefill, setText, clearPrefill]);

  return (
    <div className="dialogue">
      <div className="messages" role="log" aria-live="polite" aria-label="Conversation">
        {session.messages.map((m) => (
          <article key={m.id} className={`msg msg-${m.role}`}>
            <span className="sr-only">{m.role === "user" ? "You" : "Tutor"}: </span>
            <Prose text={m.content} />
            {m.interrupted && !m.streaming && <span className="note">Reply interrupted.</span>}
          </article>
        ))}
        {session.messages.length === 0 && cell.status !== "failed" && (
          <p className="waiting">Ask anything.</p>
        )}
        <div ref={end} />
      </div>
      {session.live && (
        <form className="composer" onSubmit={(e) => { e.preventDefault(); void submit(text); }}>
          <textarea
            ref={box} aria-label="Your message" rows={1} value={text} placeholder="Ask anything…"
            onChange={(e) => setText(e.target.value)} onFocus={() => setFocused(true)} onBlur={() => setFocused(false)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.shiftKey) { e.preventDefault(); void submit(text); }
            }}
          />
          {streaming
            ? <button type="button" onClick={() => void api.control(cell.id, "stop")}>Stop</button>
            : <button type="submit" className="primary" disabled={!text.trim() || sending}>Send</button>}
          {error && <p className="error composer-error" role="alert">{error}</p>}
        </form>
      )}
    </div>
  );
}
