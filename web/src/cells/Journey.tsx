import type { JourneyContent } from "../types";
import { Rich, type CellProps } from "./common";

const TIER: Record<string, string> = { weak: "Started", moderate: "Getting it", strong: "Strong", durable: "Remembered" };

export function Journey({ cell }: CellProps) {
  const c = cell.content as JourneyContent | undefined;
  if (!c?.topics) return null;
  return (
    <div className="journey">
      <p className={`journey-next ${c.complete ? "journey-next-done" : ""}`}><Rich text={c.next} /></p>
      <ol className="path">
        {c.topics.map((t, i) => {
          const here = i === c.current && !c.complete;
          return (
            <li key={t.title} className={t.done ? "stop stop-done" : here ? "stop stop-here" : "stop"} aria-current={here ? "step" : undefined}>
              <span className="stop-dot" aria-hidden="true">{t.done ? "✓" : i + 1}</span>
              <span className="stop-body">
                <span className="stop-title"><Rich text={t.title} /></span>
                {t.done && <span className="stop-badge">🏅 {t.milestone}</span>}
                {here && (
                  <span className="pips" aria-label={`${t.progress} of ${t.goal} steps`}>
                    {Array.from({ length: t.goal }, (_, n) => <span key={n} className={n < t.progress ? "pip pip-on" : "pip"} />)}
                  </span>
                )}
              </span>
            </li>
          );
        })}
      </ol>
      {c.shown.length > 0 && (
        <div className="shown">
          {c.shown.slice(-4).map((s) => <span key={s.concept} className={`pill tier-${s.tier}`} title={s.why}>{s.concept} · {TIER[s.tier] ?? s.tier}</span>)}
        </div>
      )}
    </div>
  );
}
