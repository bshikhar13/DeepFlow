import { useEffect, useState } from "react";
import { api } from "../api";
import type { QuizContent } from "../types";
import { Rich, errorText, type CellProps } from "./common";
import { Confetti } from "./Confetti";

const NEXT_AFTER_CORRECT_MS = 2200;

export function Quiz({ cell }: CellProps) {
  const c = cell.content as QuizContent | undefined;
  const [error, setError] = useState("");
  const [picked, setPicked] = useState<number | null>(null);
  const result = c?.result;
  const id = c?.id;
  const correct = result?.correct;

  useEffect(() => { setPicked(null); setError(""); }, [id]);

  // A correct answer moves on by itself. After a wrong one, the student reads the reason first.
  useEffect(() => {
    if (!correct) return;
    const t = setTimeout(() => void api.action(cell.id, "quiz.next", {}).catch((e) => setError(errorText(e))), NEXT_AFTER_CORRECT_MS);
    return () => clearTimeout(t);
  }, [cell.id, id, correct]);

  if (!c?.prompt) return null;

  const choose = (i: number) => {
    setPicked(i);
    setError("");
    api.action(cell.id, "quiz.answered", { questionId: c.id, choice: i }).catch((e) => {
      setPicked(null);
      setError(errorText(e));
    });
  };

  const optionClass = (i: number) => {
    if (result) {
      if (i === result.answer) return "option option-right";
      if (i === result.chosen) return "option option-wrong";
      return "option option-dim";
    }
    return picked === i ? "option option-picked" : "option";
  };

  return (
    <div className="quiz" key={c.id}>
      <div className="quiz-meta">
        <span className="pill"><Rich text={c.topic} /></span>
        <span className="grow" />
        {c.streak > 0 && <span className="streak" aria-label={`Streak: ${c.streak}`}>🔥 {c.streak}</span>}
      </div>
      <p className="quiz-q"><Rich text={c.prompt} /></p>
      <div className="options">
        {c.options.map((o, i) => (
          <button key={i} className={optionClass(i)} disabled={!!result || picked !== null} onClick={() => choose(i)}><Rich text={o} /></button>
        ))}
      </div>
      {result && (
        <div className={`quiz-why ${result.correct ? "quiz-why-right" : "quiz-why-wrong"}`} role="status">
          <b>{result.correct ? "Yes!" : "Not quite."}</b> <span><Rich text={result.why} /></span>
          {!result.correct && <button className="primary" onClick={() => void api.action(cell.id, "quiz.next", {})}>Next question</button>}
        </div>
      )}
      {result?.correct && <Confetti />}
      {error && <p className="error" role="alert">{error}</p>}
    </div>
  );
}
