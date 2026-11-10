import katex from "katex";
import { Fragment, useMemo, useState, type ReactNode } from "react";
import { ApiError } from "../api";
import type { Cell, SessionState } from "../types";

export interface CellProps {
  cell: Cell;
  session: SessionState;
  prefill: string;
  clearPrefill: () => void;
  discuss: (text: string) => void;
}

export function errorText(e: unknown): string {
  return e instanceof ApiError ? e.message : "Something went wrong. Your text is still here.";
}

const MATH = /\\\((.+?)\\\)|\\\[([\s\S]+?)\\\]|\$\$([\s\S]+?)\$\$|(?<![\\\w$])\$(?=\S)([^$\n]+?)(?<=\S)\$(?![\d$])/g;

function Math({ tex, display }: { tex: string; display: boolean }) {
  const html = useMemo(() => {
    try {
      // KaTeX's output is safe here: "trust" is off, so it can't produce links or
      // scripts from what a model writes. This is the only raw HTML in the app.
      return katex.renderToString(tex, { displayMode: display, throwOnError: false, trust: false, strict: false, maxSize: 20, maxExpand: 200 });
    } catch {
      return null;
    }
  }, [tex, display]);
  if (html === null) return <code>{tex}</code>;
  return <span dangerouslySetInnerHTML={{ __html: html }} />;
}

function withMath(text: string): ReactNode {
  const out: ReactNode[] = [];
  let at = 0;
  for (const m of text.matchAll(MATH)) {
    if (m.index > at) out.push(<Fragment key={at}>{text.slice(at, m.index)}</Fragment>);
    const tex = m[1] ?? m[2] ?? m[3] ?? m[4];
    out.push(<Math key={m.index} tex={tex} display={m[2] !== undefined || m[3] !== undefined} />);
    at = m.index + m[0].length;
  }
  if (at < text.length) out.push(<Fragment key={at}>{text.slice(at)}</Fragment>);
  return out;
}

// Rich renders **bold**, `code` and maths as React nodes. Model text is never
// treated as HTML.
export function Rich({ text }: { text: string }) {
  const parts = text.split(/(\*\*[^*]+\*\*|`[^`]+`)/g);
  return (
    <>
      {parts.map((p, i) => {
        if (p.startsWith("**") && p.endsWith("**") && p.length > 4) return <strong key={i}>{withMath(p.slice(2, -2))}</strong>;
        if (p.startsWith("`") && p.endsWith("`") && p.length > 2) return <code key={i}>{p.slice(1, -1)}</code>;
        return <Fragment key={i}>{withMath(p)}</Fragment>;
      })}
    </>
  );
}

// plain removes maths delimiters, for labels that can't hold rendered maths.
export function plain(text: string): string {
  return text.replace(/\\[()[\]]|\$\$?/g, "");
}

export function Prose({ text }: { text: string }) {
  const blocks = text.split(/\n{2,}/);
  return (
    <>
      {blocks.map((b, i) => {
        const lines = b.split("\n");
        if (lines.every((l) => /^\s*[-*] /.test(l))) {
          return <ul key={i}>{lines.map((l, j) => <li key={j}><Rich text={l.replace(/^\s*[-*] /, "")} /></li>)}</ul>;
        }
        return <p key={i}><Rich text={b} /></p>;
      })}
    </>
  );
}

// Submitter is a free-text box with autosaved draft and a busy lease.
export function useSubmit(send: (text: string) => Promise<void>, clear: () => void) {
  const [error, setError] = useState("");
  const [sending, setSending] = useState(false);
  const submit = async (text: string) => {
    if (!text.trim() || sending) return;
    setSending(true);
    setError("");
    try {
      await send(text);
      clear();
    } catch (e) {
      setError(errorText(e));
    } finally {
      setSending(false);
    }
  };
  return { error, sending, submit };
}

export function Waiting({ children }: { children: ReactNode }) {
  return <p className="waiting">{children}</p>;
}
