import { useEffect, useState } from "react";
import { api } from "./api";
import type { Status } from "./types";
import { ConsentNotice } from "./views/ConsentNotice";
import { Home } from "./views/Home";
import { WorkspaceView } from "./views/Workspace";
import { usePrefs } from "./prefs";

function useHash() {
  const [hash, setHash] = useState(location.hash);
  useEffect(() => {
    const on = () => setHash(location.hash);
    addEventListener("hashchange", on);
    return () => removeEventListener("hashchange", on);
  }, []);
  return hash;
}

export function App() {
  const hash = useHash();
  const prefs = usePrefs();
  const [status, setStatus] = useState<Status | null>(null);
  const [error, setError] = useState("");
  const [notice, setNotice] = useState("");

  useEffect(() => {
    api.status().then(setStatus).catch(() => setError("Can't reach the DeepFlow server."));
  }, []);

  if (error) return <main className="center"><p className="error" role="alert">{error}</p></main>;
  if (!status) return <main className="center"><p className="waiting">Loading…</p></main>;
  if (status.consentRequired) {
    return <ConsentNotice status={status} onAccept={() => api.consent().then(() => setStatus({ ...status, consentRequired: false }))} />;
  }
  const m = /^#\/s\/(.+)$/.exec(hash);
  if (m) {
    return (
      <WorkspaceView
        key={m[1]} sessionId={m[1]} status={status} prefs={prefs}
        onExit={(msg) => { setNotice(msg ?? ""); location.hash = "#/"; }}
      />
    );
  }
  return <Home status={status} prefs={prefs} notice={notice} clearNotice={() => setNotice("")} />;
}
