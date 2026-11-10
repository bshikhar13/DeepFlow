import { useEffect, useState } from "react";

function read(key: string, fallback: string): string {
  try { return localStorage.getItem(key) ?? fallback; } catch { return fallback; }
}

function write(key: string, value: string) {
  try { localStorage.setItem(key, value); } catch { /* preferences are optional */ }
}

export type Theme = "dark" | "light";

export function usePrefs() {
  const [theme, setTheme] = useState<Theme>(() => (read("df.theme", "dark") === "light" ? "light" : "dark"));
  const [scale, setScale] = useState<number>(() => Number(read("df.scale", "1")) || 1);
  useEffect(() => {
    document.documentElement.dataset.theme = theme;
    write("df.theme", theme);
  }, [theme]);
  useEffect(() => {
    document.documentElement.style.setProperty("--scale", String(scale));
    write("df.scale", String(scale));
  }, [scale]);
  return {
    theme,
    toggleTheme: () => setTheme((t) => (t === "dark" ? "light" : "dark")),
    bigger: () => setScale((s) => Math.min(1.4, +(s + 0.1).toFixed(1))),
    smaller: () => setScale((s) => Math.max(0.8, +(s - 0.1).toFixed(1))),
  };
}
export type Prefs = ReturnType<typeof usePrefs>;
