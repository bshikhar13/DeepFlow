import { act, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MilestoneBanner } from "./MilestoneBanner";

const m = { title: "Force", milestone: "Force Finder", next: "Friction", complete: false };

afterEach(() => vi.useRealTimers());

describe("MilestoneBanner", () => {
  it("celebrates only new milestones, then gets out of the way", () => {
    vi.useFakeTimers();
    const { rerender } = render(<MilestoneBanner milestone={m} count={0} />);
    expect(screen.queryByText("Force Finder")).toBeNull();
    rerender(<MilestoneBanner milestone={m} count={1} />);
    expect(screen.getByText("Force Finder")).toBeInTheDocument();
    expect(screen.getByText("Next: Friction")).toBeInTheDocument();
    act(() => { vi.advanceTimersByTime(5100); });
    expect(screen.queryByText("Force Finder")).toBeNull();
  });
});
