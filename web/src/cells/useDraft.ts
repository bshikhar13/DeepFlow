import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "../api";

const DEBOUNCE_MS = 800;

// useDraft keeps learner text and autosaves it, so a crash can't lose it.
export function useDraft(cellId: string, initial = "") {
  const [text, setTextState] = useState(initial);
  const timer = useRef<ReturnType<typeof setTimeout>>();

  useEffect(() => () => clearTimeout(timer.current), []);

  // A draft restored by undo arrives after mount. Never overwrite newer typing.
  useEffect(() => {
    if (initial) setTextState((cur) => cur || initial);
  }, [initial]);

  const setText = useCallback((v: string) => {
    setTextState(v);
    clearTimeout(timer.current);
    timer.current = setTimeout(() => void api.draft(cellId, v).catch(() => {}), DEBOUNCE_MS);
  }, [cellId]);

  const clear = useCallback(() => {
    setTextState("");
    clearTimeout(timer.current);
    void api.draft(cellId, "").catch(() => {});
  }, [cellId]);

  return { text, setText, clear };
}
