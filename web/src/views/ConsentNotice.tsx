import type { Status } from "../types";

export function ConsentNotice({ status, onAccept }: { status: Status; onAccept: () => void }) {
  return (
    <main className="center">
      <div className="notice-card">
        <h1>Before you start</h1>
        <p>DeepFlow keeps your learning history on this computer.</p>
        <p>To generate dialogue and exercises, it sends parts of what you write and your learning context to {status.vendor}, using the <code>{status.model}</code> model.</p>
        {status.provider === "openrouter" && <p>OpenRouter passes it on to the company behind that model, and only uses services that don't train on your data.</p>}
        <p>Nothing is sent until you continue. To work offline with canned content instead, restart DeepFlow with <code>DEEPFLOW_PROVIDER=mock</code>.</p>
        <button className="primary" onClick={onAccept}>Continue with {status.vendor}</button>
      </div>
    </main>
  );
}
