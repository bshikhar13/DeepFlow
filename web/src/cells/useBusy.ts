import { useEffect } from "react";
import { api } from "../api";

const RENEW_MS = 10_000;

// useBusy holds a lease that tells the Orchestrator this cell is in active use.
// The server's lease expires by itself, so a closed tab can't freeze adaptation.
export function useBusy(cellId: string, active: boolean) {
  useEffect(() => {
    if (!active) return;
    const send = (busy: boolean) => void api.busy(cellId, busy).catch(() => {});
    send(true);
    const t = setInterval(() => send(true), RENEW_MS);
    return () => {
      clearInterval(t);
      send(false);
    };
  }, [cellId, active]);
}
