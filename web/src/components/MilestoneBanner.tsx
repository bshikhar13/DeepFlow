import { useEffect, useState } from "react";
import { Rich } from "../cells/common";
import { Confetti } from "../cells/Confetti";
import type { Milestone } from "../types";

const VISIBLE_MS = 5000;

export function MilestoneBanner({ milestone, count }: { milestone?: Milestone; count: number }) {
  const [seen, setSeen] = useState(count);
  useEffect(() => {
    if (count === seen) return;
    const t = setTimeout(() => setSeen(count), VISIBLE_MS);
    return () => clearTimeout(t);
  }, [count, seen]);
  if (!milestone || count === seen) return null;
  return (
    <div className="milestone" role="status">
      <Confetti pieces={36} />
      <span className="milestone-medal" aria-hidden="true">🏅</span>
      <p className="milestone-name"><Rich text={milestone.milestone} /></p>
      <p>{milestone.complete ? "Journey complete!" : <Rich text={`Next: ${milestone.next}`} />}</p>
    </div>
  );
}
