import { act, renderHook } from "@testing-library/react";
import { beforeEach, describe, expect, it } from "vitest";
import { usePrefs } from "./prefs";

beforeEach(() => {
  localStorage.clear();
  delete document.documentElement.dataset.theme;
});

describe("theme preference", () => {
  it("defaults to black", () => {
    const { result } = renderHook(() => usePrefs());
    expect(result.current.theme).toBe("dark");
    expect(document.documentElement.dataset.theme).toBe("dark");
  });
  it("toggles between black and white, and remembers", () => {
    const { result, unmount } = renderHook(() => usePrefs());
    act(() => result.current.toggleTheme());
    expect(document.documentElement.dataset.theme).toBe("light");
    unmount();
    const again = renderHook(() => usePrefs());
    expect(again.result.current.theme).toBe("light");
    act(() => again.result.current.toggleTheme());
    expect(again.result.current.theme).toBe("dark");
  });
  it("treats the old system setting as black", () => {
    localStorage.setItem("df.theme", "system");
    const { result } = renderHook(() => usePrefs());
    expect(result.current.theme).toBe("dark");
  });
});
