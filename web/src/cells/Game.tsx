import { useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api";
import { parseExpr } from "../lib/expr";
import type { GameContent, GameParam } from "../types";
import { Rich, errorText, plain, type CellProps } from "./common";
import { Confetti } from "./Confetti";
import { LocateGame } from "./Map";
import { useBusy } from "./useBusy";

const HOLD_TO_WIN_MS = 700;
const PLAYING_FOR_MS = 8000;

// usePlaying holds the busy lease while the student is playing, so the
// Orchestrator doesn't swap the game out from under them.
function usePlaying(cellId: string) {
  const [playing, setPlaying] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout>>();
  useBusy(cellId, playing);
  useEffect(() => () => clearTimeout(timer.current), []);
  return () => {
    setPlaying(true);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => setPlaying(false), PLAYING_FOR_MS);
  };
}

const fmt = (v: number) => String(Number(v.toFixed(2)));

function Stars({ count }: { count: number }) {
  if (count === 0) return null;
  return <span className="stars" aria-label={`${count} stars`}>⭐ {count}</span>;
}

export function Game({ cell }: CellProps) {
  const c = cell.content as GameContent | undefined;
  const [error, setError] = useState("");
  const touch = usePlaying(cell.id);
  if (!c?.title) return null;
  const win = (attempt: Attempt) => {
    setError("");
    api.action(cell.id, "game.level_won", attempt).catch((e) => setError(errorText(e)));
  };
  const next = () => void api.action(cell.id, "game.next", {}).catch((e) => setError(errorText(e)));
  return (
    <div className="game">
      <div className="game-head">
        <div>
          <h3><Rich text={c.title} /></h3>
          <p className="game-goal"><Rich text={c.goal} /></p>
        </div>
        <Stars count={c.stars} />
      </div>
      {c.kind === "locate" && <LocateGame key={c.id ?? c.title} game={c} onWin={win} onTouch={touch} done={<Done onNext={next} loading={cell.status === "updating"} />} />}
      {c.kind === "target"
        ? <TargetGame key={c.id ?? c.title} game={c} onWin={win} onNext={next} onTouch={touch} loading={cell.status === "updating"} />
        : c.kind === "sort" && <SortGame key={c.id ?? c.title} game={c} onWin={win} onNext={next} onTouch={touch} loading={cell.status === "updating"} />}
      {error && <p className="error" role="alert">{error}</p>}
    </div>
  );
}

// Attempt is sent with a win so the server can check it for itself.
type Attempt = { level: number; values?: Record<string, number>; order?: string[]; placed?: Record<string, string>; point?: { lat: number; lon: number } };

interface GameProps { game: GameContent; onWin: (a: Attempt) => void; onNext: () => void; onTouch: () => void; loading: boolean }

function Done({ onNext, loading }: { onNext: () => void; loading: boolean }) {
  return (
    <div className="game-done" role="status">
      <Confetti />
      <span className="game-done-mark" aria-hidden="true">🎉</span>
      <p><b>You did it!</b></p>
      <button className="primary" onClick={onNext} disabled={loading}>{loading ? "Building…" : "New game"}</button>
    </div>
  );
}

// corners evaluates the model at each slider extreme, to size the gauge.
function gaugeRange(f: (v: Record<string, number>) => number, params: GameParam[], targets: number[]) {
  let lo = Math.min(0, ...targets);
  let hi = Math.max(...targets, 1);
  const n = 1 << params.length;
  for (let mask = 0; mask < n; mask++) {
    const vars: Record<string, number> = {};
    params.forEach((p, i) => { vars[p.id] = mask & (1 << i) ? p.max : p.min; });
    const v = f(vars);
    if (Number.isFinite(v)) {
      lo = Math.min(lo, v);
      hi = Math.max(hi, v);
    }
  }
  hi = Math.min(hi, Math.max(...targets, 1) * 2.5);
  return { lo, hi: hi > lo ? hi : lo + 1 };
}

function TargetGame({ game, onWin, onNext, onTouch, loading }: GameProps) {
  const [values, setValues] = useState<Record<string, number>>(() => Object.fromEntries(game.params.map((p) => [p.id, p.value])));
  const f = useMemo(() => parseExpr(game.output.expr, game.params.map((p) => p.id)), [game.output.expr, game.params]);
  const level = game.levels.findIndex((_, i) => !game.won.includes(i));
  const sent = useRef(new Set<number>());
  const value = f(values);
  const finite = Number.isFinite(value);
  const goal = game.levels[level];
  const onTarget = !!goal && finite && Math.abs(value - goal.target) <= goal.tolerance;

  useEffect(() => {
    if (!onTarget || sent.current.has(level)) return;
    const t = setTimeout(() => {
      sent.current.add(level);
      onWin({ level, values });
    }, HOLD_TO_WIN_MS);
    return () => clearTimeout(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [onTarget, level, value]);

  const { lo, hi } = useMemo(() => gaugeRange(f, game.params, game.levels.map((l) => l.target)), [f, game.params, game.levels]);
  const pct = (v: number) => Math.max(0, Math.min(100, ((v - lo) / (hi - lo)) * 100));

  return (
    <>
      {goal ? (
        <>
          <div className="target-readout">
            <span className="level-tag">Level {level + 1} of {game.levels.length}</span>
            <span className="readout-value" data-testid="game-value">{finite ? fmt(value) : "—"}</span>
            <span className="readout-unit">{game.output.unit}</span>
            <span className="grow" />
            <span className="readout-target">🎯 <b data-testid="game-target">{fmt(goal.target)}</b> {game.output.unit}</span>
          </div>
          <div className={`gauge ${onTarget ? "gauge-hit" : ""}`} role="img" aria-label={`${plain(game.output.label)}: ${finite ? fmt(value) : "undefined"}, target ${fmt(goal.target)}`}>
            <div className="gauge-zone" style={{ left: `${pct(goal.target - goal.tolerance)}%`, width: `${Math.max(1.5, pct(goal.target + goal.tolerance) - pct(goal.target - goal.tolerance))}%` }} />
            <div className="gauge-fill" style={{ width: `${finite ? pct(value) : 0}%` }} />
          </div>
          <p className="gauge-label"><Rich text={game.output.label} /></p>
        </>
      ) : <Done onNext={onNext} loading={loading} />}
      {goal && (
        <div className="sliders">
          {game.params.map((p) => (
            <label key={p.id} className="slider">
              <span><Rich text={p.label} /> <b>{fmt(values[p.id])}{p.unit ? ` ${p.unit}` : ""}</b></span>
              <input type="range" min={p.min} max={p.max} step={p.step} value={values[p.id]}
                onChange={(e) => { onTouch(); setValues((v) => ({ ...v, [p.id]: Number(e.target.value) })); }} />
            </label>
          ))}
        </div>
      )}
      <p className="game-note">
        {goal?.hint && <span>💡 <Rich text={goal.hint} /> </span>}
        {game.note && <span className="muted">Simplified: <Rich text={game.note} /></span>}
      </p>
    </>
  );
}

function SortGame({ game, onWin, onNext, onTouch, loading }: GameProps) {
  const solved = game.won.includes(0);
  return solved
    ? <Done onNext={onNext} loading={loading} />
    : game.mode === "order"
      ? <OrderGame game={game} onWin={onWin} onTouch={onTouch} />
      : <GroupGame game={game} onWin={onWin} onTouch={onTouch} />;
}

// startOrder rotates the correct order. With distinct items, a non-zero rotation
// always has at least one out of place, so it never starts solved.
function startOrder(labels: string[], seed: string) {
  const k = 1 + (seed.length % (labels.length - 1));
  return [...labels.slice(k), ...labels.slice(0, k)];
}

function OrderGame({ game, onWin, onTouch }: Pick<GameProps, "game" | "onWin" | "onTouch">) {
  const answer = game.items.map((i) => i.label);
  const [list, setList] = useState(() => startOrder(answer, game.title));
  const [note, setNote] = useState("");
  const [dragged, setDragged] = useState<number | null>(null);
  const move = (from: number, to: number) => {
    if (to < 0 || to >= list.length) return;
    onTouch();
    const next = [...list];
    const [item] = next.splice(from, 1);
    next.splice(to, 0, item);
    setList(next);
    setNote("");
  };
  const check = () => {
    const right = list.filter((l, i) => l === answer[i]).length;
    if (right === list.length) onWin({ level: 0, order: list });
    else setNote(`Not quite: ${right} of ${list.length} in place.`);
  };
  return (
    <>
      <ol className="sort-list">
        {list.map((label, i) => (
          <li key={label} className={`sort-item ${dragged === i ? "sort-dragging" : ""}`} draggable
            onDragStart={() => setDragged(i)} onDragEnd={() => setDragged(null)}
            onDragOver={(e) => e.preventDefault()}
            onDrop={(e) => { e.preventDefault(); if (dragged !== null) move(dragged, i); setDragged(null); }}>
            <span className="sort-pos" aria-hidden="true">{i + 1}</span>
            <span className="sort-label" data-testid="sort-label"><Rich text={label} /></span>
            <button aria-label={`Move ${plain(label)} up`} disabled={i === 0} onClick={() => move(i, i - 1)}>▲</button>
            <button aria-label={`Move ${plain(label)} down`} disabled={i === list.length - 1} onClick={() => move(i, i + 1)}>▼</button>
          </li>
        ))}
      </ol>
      <div className="row">
        <button className="primary" onClick={check}>Check</button>
        {note && <span className="game-feedback" role="status">{note}</span>}
      </div>
    </>
  );
}

function GroupGame({ game, onWin, onTouch }: Pick<GameProps, "game" | "onWin" | "onTouch">) {
  const [placed, setPlaced] = useState<Record<string, string>>({});
  const [note, setNote] = useState("");
  const done = game.items.every((i) => placed[i.label]);
  const check = () => {
    const right = game.items.filter((i) => placed[i.label] === i.group).length;
    if (right === game.items.length) onWin({ level: 0, placed });
    else setNote(`Not quite: ${right} of ${game.items.length} right.`);
  };
  return (
    <>
      <ul className="group-list">
        {game.items.map((it) => (
          <li key={it.label} className="group-item">
            <span className="sort-label" data-testid="sort-label"><Rich text={it.label} /></span>
            <span className="group-choices">
              {game.groups.map((g) => (
                <button key={g} className="chip" aria-pressed={placed[it.label] === g} aria-label={`Put ${plain(it.label)} in ${plain(g)}`}
                  onClick={() => { onTouch(); setPlaced((p) => ({ ...p, [it.label]: g })); setNote(""); }}><Rich text={g} /></button>
              ))}
            </span>
          </li>
        ))}
      </ul>
      <div className="row">
        <button className="primary" disabled={!done} onClick={check}>Check</button>
        {note && <span className="game-feedback" role="status">{note}</span>}
      </div>
    </>
  );
}
