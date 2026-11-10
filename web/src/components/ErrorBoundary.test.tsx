import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { CardBoundary } from "./CardBoundary";

function Bomb({ armed }: { armed: boolean }) {
  if (armed) throw new Error("boom");
  return <p>Fine</p>;
}

describe("CardBoundary", () => {
  it("keeps a crash inside one card, with a way back", async () => {
    vi.spyOn(console, "error").mockImplementation(() => {});
    function Host() {
      const [armed, setArmed] = useState(true);
      return (
        <>
          <p>Other card</p>
          <button onClick={() => setArmed(false)}>Disarm</button>
          <CardBoundary><Bomb armed={armed} /></CardBoundary>
        </>
      );
    }
    render(<Host />);
    expect(screen.getByText("Other card")).toBeInTheDocument();
    expect(screen.getByRole("alert")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Disarm" }));
    await userEvent.click(screen.getByRole("button", { name: /try again/i }));
    expect(screen.getByText("Fine")).toBeInTheDocument();
  });
});
