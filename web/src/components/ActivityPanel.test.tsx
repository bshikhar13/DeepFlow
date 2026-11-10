import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ActivityPanel } from "./ActivityPanel";

const decided = { decision: { action: "REPLACE", reason: "moved on", unsolicited: true }, canUndo: true, at: "" };

describe("ActivityPanel", () => {
  it("keeps undo available after the toast is gone", async () => {
    const onUndo = vi.fn().mockResolvedValue(undefined);
    render(<ActivityPanel decisions={[decided]} artifacts={[]} onClose={() => {}} onUndo={onUndo} />);
    await userEvent.click(screen.getByRole("button", { name: /undo the last change/i }));
    expect(onUndo).toHaveBeenCalledOnce();
  });
  it("says so when there is nothing left to undo", async () => {
    const onUndo = vi.fn().mockRejectedValue(new Error("nothing"));
    render(<ActivityPanel decisions={[decided]} artifacts={[]} onClose={() => {}} onUndo={onUndo} />);
    await userEvent.click(screen.getByRole("button", { name: /undo the last change/i }));
    await waitFor(() => expect(screen.getByText(/nothing left to undo/i)).toBeInTheDocument());
  });
  it("offers no undo before any change", () => {
    render(<ActivityPanel decisions={[]} artifacts={[]} onClose={() => {}} onUndo={async () => {}} />);
    expect(screen.queryByRole("button", { name: /undo/i })).toBeNull();
  });
});
