import { useEffect, useState } from "react";
import { ApiError, api } from "../api";
import type { Authorship, ConceptObject, Evidence, ObjectType } from "../types";
import { Rich, errorText, type CellProps } from "./common";
import { useBusy } from "./useBusy";

const AUTHOR: Record<Authorship, string> = { ai: "AI", learner: "You", edited: "You", reference: "Source" };
const GROUPS: { type: ObjectType; title: string }[] = [
  { type: "insight", title: "💡 Got it" }, { type: "hypothesis", title: "🧪 My ideas" }, { type: "question", title: "❓ Questions" },
  { type: "concept", title: "📌 Concepts" }, { type: "connection", title: "🔗 Links" },
];

function ObjectItem({ o, live, onChanged, discuss, cellId }: { o: ConceptObject; live: boolean; onChanged: () => void; discuss: (t: string) => void; cellId: string }) {
  const [mode, setMode] = useState<"view" | "edit" | "challenge">("view");
  const [text, setText] = useState(o.content);
  const [version, setVersion] = useState(o.version);
  const [error, setError] = useState("");
  const [conflict, setConflict] = useState<ConceptObject | null>(null);
  const [evidence, setEvidence] = useState<Evidence[] | null>(null);
  useBusy(cellId, mode !== "view");

  // Another tab saved while this one is editing: keep the text and say so.
  useEffect(() => {
    if (o.version > version) {
      if (mode === "edit") setConflict(o);
      setVersion(o.version);
    }
  }, [o, version, mode]);

  const save = async () => {
    setError("");
    try {
      await api.patchObject(o.id, text, version);
      setMode("view");
      setConflict(null);
      onChanged();
    } catch (e) {
      if (e instanceof ApiError && e.code === "version_conflict") {
        const cur = (e.body as { current: ConceptObject }).current;
        setConflict(cur);
        setVersion(cur.version);
      } else setError(errorText(e));
    }
  };
  const challenge = async () => {
    setError("");
    try {
      await api.challengeObject(o.id, text);
      setMode("view");
      setText(o.content);
      onChanged();
    } catch (e) { setError(errorText(e)); }
  };

  return (
    <li className={`object author-${o.authorship}`}>
      {mode === "view" && <p><Rich text={o.content} /></p>}
      {mode === "edit" && (
        <label className="field"><span className="sr-only">Edit this {o.type}</span>
          <textarea rows={3} value={text} onChange={(e) => setText(e.target.value)} />
        </label>
      )}
      {mode === "challenge" && (
        <>
          <p><Rich text={o.content} /></p>
          <label className="field"><span>What's wrong?</span>
            <textarea rows={2} value={text} onChange={(e) => setText(e.target.value)} />
          </label>
        </>
      )}
      {conflict && <p className="error" role="alert">Changed elsewhere: “{conflict.content}”. Save again to replace it.</p>}
      {error && <p className="error" role="alert">{error}</p>}
      <div className="object-meta">
        <span className="author">{AUTHOR[o.authorship]}</span>
        {o.status === "challenged" && <span className="badge">Disputed</span>}
        <span className="grow" />
        {mode === "view" ? (
          <>
            <button className="link" onClick={() => { setText(o.content); setVersion(o.version); setMode("edit"); }}>Edit</button>
            <button className="link" onClick={() => { setText(""); setMode("challenge"); }}>Disagree</button>
            {live && <button className="link" onClick={() => discuss(`About this: "${o.content}". `)}>Ask</button>}
            <button className="link" onClick={() => void api.objectEvidence(o.id).then(setEvidence).catch(() => setEvidence([]))}>Evidence</button>
          </>
        ) : (
          <>
            <button className="link strong" onClick={() => void (mode === "edit" ? save() : challenge())} disabled={!text.trim()}>{mode === "edit" ? "Save" : "Send"}</button>
            <button className="link" onClick={() => { setMode("view"); setConflict(null); setError(""); }}>Cancel</button>
          </>
        )}
      </div>
      {evidence && (
        <div className="note">
          {evidence.length === 0 ? "Nothing linked yet." : evidence.map((e) => <p key={e.id}>{e.tier} evidence on {e.concept}: {e.rationale}</p>)}
        </div>
      )}
    </li>
  );
}

export function Document({ cell, session, discuss }: CellProps) {
  const [objects, setObjects] = useState<ConceptObject[] | null>(null);
  const [rev, setRev] = useState(0);
  const pursuitId = session.pursuit?.id;
  useEffect(() => {
    if (pursuitId) void api.listObjects(pursuitId).then(setObjects).catch(() => {});
  }, [pursuitId, session.documentRev, rev]);
  if (objects === null) return null;
  if (objects.length === 0) return <p className="waiting">Your ideas will land here.</p>;
  return (
    <div className="document">
      {GROUPS.map((g) => {
        const items = objects.filter((o) => o.type === g.type);
        if (items.length === 0) return null;
        return (
          <section key={g.type}>
            <h4>{g.title}</h4>
            <ul className="objects">
              {items.map((o) => <ObjectItem key={o.id} o={o} live={session.live} discuss={discuss} cellId={cell.id} onChanged={() => setRev((r) => r + 1)} />)}
            </ul>
          </section>
        );
      })}
    </div>
  );
}
