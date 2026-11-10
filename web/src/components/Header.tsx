import type { Prefs } from "../prefs";
import type { Status } from "../types";

export function ProviderBadge({ status }: { status: Status }) {
  if (status.provider === "mock") {
    return <span className="badge" title="No model is connected. Content is canned, and nothing leaves this computer.">Mock model</span>;
  }
  return <span className="badge" title={`Learning content is sent to ${status.vendor} for inference. Your history stays on this computer.`}>{status.model} · sends to {status.vendor}</span>;
}

interface Props {
  title: string; inquiry: string; status: Status; prefs: Prefs; connected: boolean; ending: boolean; activityOpen: boolean;
  onActivity: () => void; onHome: () => void; onEnd: () => void;
}

export function Header({ title, inquiry, status, prefs, connected, ending, activityOpen, onActivity, onHome, onEnd }: Props) {
  return (
    <header className="top">
      <button className="wordmark-btn" onClick={onHome} aria-label="Back to pursuits">DeepFlow</button>
      <div className="top-title">
        <h1>{title}</h1>
              </div>
      <div className="row top-controls">
        {!connected && <span className="badge" role="status">Reconnecting…</span>}
        <ProviderBadge status={status} />
        <button onClick={prefs.smaller} aria-label="Smaller text">A−</button>
        <button onClick={prefs.bigger} aria-label="Larger text">A+</button>
        <button onClick={prefs.toggleTheme} aria-label={prefs.theme === "dark" ? "White theme" : "Black theme"}>{prefs.theme === "dark" ? "☀️" : "🌙"}</button>
        <button onClick={onActivity} aria-expanded={activityOpen}>Activity</button>
        <button className="primary" onClick={onEnd} disabled={ending}>{ending ? "Saving…" : "Finish"}</button>
      </div>
    </header>
  );
}
