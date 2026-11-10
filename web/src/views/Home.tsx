import { useCallback, useEffect, useState } from "react";
import { ApiError, api } from "../api";
import type { Prefs } from "../prefs";
import type { Pursuit, Session, Status } from "../types";
import { ProviderBadge } from "../components/Header";

interface Row { pursuit: Pursuit; last?: Session; count: number }

export function Home({ status, prefs, notice, clearNotice }: { status: Status; prefs: Prefs; notice: string; clearNotice: () => void }) {
  const [rows, setRows] = useState<Row[] | null>(null);
  const [title, setTitle] = useState("");
  const [inquiry, setInquiry] = useState("");
  const [grade, setGrade] = useState(8);
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const [confirming, setConfirming] = useState<string | null>(null);

  const load = useCallback(async () => {
    const list = await api.listPursuits();
    const details = await Promise.all(list.map((p) => api.getPursuit(p.id)));
    setRows(details.map((d) => ({ pursuit: d.pursuit, last: d.sessions[d.sessions.length - 1], count: d.sessions.length })));
  }, []);
  useEffect(() => { void load().catch(() => setError("Couldn't load your pursuits.")); }, [load]);

  const open = async (pursuitId: string, inq: string) => {
    setBusy(true);
    setError("");
    clearNotice();
    try {
      const s = await api.startSession(pursuitId, inq);
      location.hash = `#/s/${s.session!.id}`;
    } catch (e) {
      setError(e instanceof ApiError ? e.message : "Couldn't start the session.");
    } finally {
      setBusy(false);
    }
  };

  const create = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!title.trim() || busy) return;
    try {
      const p = await api.createPursuit(title, inquiry, grade);
      await open(p.id, inquiry);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : "Couldn't create the pursuit.");
    }
  };

  const remove = async (id: string) => {
    await api.deletePursuit(id).catch(() => setError("Couldn't delete that pursuit."));
    setConfirming(null);
    await load();
  };

  return (
    <main className="home">
      <header className="home-head">
        <div className="wordmark">DeepFlow</div>
        <div className="row">
          <ProviderBadge status={status} />
          <button onClick={prefs.toggleTheme} aria-label={prefs.theme === "dark" ? "White theme" : "Black theme"}>{prefs.theme === "dark" ? "☀️" : "🌙"}</button>
        </div>
      </header>
      {notice && <p className="banner" role="status">{notice}</p>}
      <section className="start">
        <h1>What do you want to learn today?</h1>
        <form onSubmit={create} className="start-form">
          <label className="field field-grow"><span className="sr-only">Subject</span>
            <input value={title} onChange={(e) => setTitle(e.target.value)} placeholder="Forces, fractions, the Mughal Empire…" maxLength={200} autoFocus />
          </label>
          <label className="field"><span className="sr-only">Your class</span>
            <select value={grade} onChange={(e) => setGrade(Number(e.target.value))}>
              {[6, 7, 8, 9, 10, 11, 12].map((g) => <option key={g} value={g}>Class {g}</option>)}
            </select>
          </label>
          <button type="submit" className="primary start-go" disabled={!title.trim() || busy}>Let's go</button>
        </form>
        {error && <p className="error" role="alert">{error}</p>}
      </section>
      {rows && rows.length > 0 && (
        <section className="pursuits">
          <h2>Keep going</h2>
          <ul>
            {rows.map(({ pursuit: p, last, count }) => {
              const ended = !!last?.endedAt && !last.endedAt.startsWith("0001");
              return (
                <li key={p.id}>
                  <div className="pursuit-main">
                    <h3>{p.title}</h3>
                    {last?.summary && <p className="note">{last.summary}</p>}
                    <p className="note">Class {p.grade} · {count === 1 ? "1 session" : `${count} sessions`}</p>
                  </div>
                  <div className="row">
                    <button className="primary" disabled={busy} onClick={() => void open(p.id, "")}>{ended ? "Return" : "Resume"}</button>
                    <a className="button" href={`/api/pursuits/${p.id}/export`} download>Export</a>
                    {confirming === p.id ? (
                      <>
                        <span className="note">Delete it all? This can't be undone.</span>
                        <button className="danger" onClick={() => void remove(p.id)}>Delete</button>
                        <button onClick={() => setConfirming(null)}>Keep</button>
                      </>
                    ) : <button onClick={() => setConfirming(p.id)}>Delete…</button>}
                  </div>
                </li>
              );
            })}
          </ul>
        </section>
      )}
    </main>
  );
}
