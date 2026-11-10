import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { Prose, Rich } from "./common";

const math = (c: HTMLElement) => c.querySelectorAll(".katex").length;

describe("maths rendering", () => {
  it("renders inline \\( \\) maths", () => {
    const { container } = render(<Rich text={"Here \\(Z=3+4i\\) ohms"} />);
    expect(math(container)).toBe(1);
    expect(container.textContent).not.toContain("\\(");
    expect(container.textContent).toContain("ohms");
  });
  it("renders display \\[ \\] maths", () => {
    const { container } = render(<Prose text={"So \\[\\sqrt{3^2+4^2}=5\\] ohms"} />);
    expect(container.querySelector(".katex-display")).not.toBeNull();
  });
  it("renders $...$ and $$...$$", () => {
    const { container } = render(<Rich text={"Try $x^2$ and $$y^2$$"} />);
    expect(math(container)).toBe(2);
  });
  it("leaves money alone", () => {
    const { container } = render(<Rich text={"It costs $5 and then $10 more"} />);
    expect(math(container)).toBe(0);
    expect(container.textContent).toContain("$5 and then $10");
  });
  it("shows a half-streamed formula as text until it closes", () => {
    const { container } = render(<Prose text={"Size is \\(Z=3"} />);
    expect(math(container)).toBe(0);
    expect(container.textContent).toContain("Z=3");
  });
  it("keeps bold working beside maths", () => {
    const { container } = render(<Prose text={"**impedance** is \\(Z\\)"} />);
    expect(container.querySelector("strong")?.textContent).toBe("impedance");
    expect(math(container)).toBe(1);
  });
  it("shows bad maths as text rather than crashing", () => {
    render(<Rich text={"Bad \\(\\frac{1\\) end"} />);
    expect(screen.getByText(/end/)).toBeInTheDocument();
  });
  it("can't be used to inject HTML or links", () => {
    const { container } = render(<Rich text={"\\(\\href{javascript:alert(1)}{x}\\) <img src=x onerror=alert(1)> \\(\\htmlId{a}{b}\\)"} />);
    expect(container.querySelector("img")).toBeNull();
    expect(container.querySelector("a")).toBeNull();
    expect(container.querySelector("[href], [onerror], script")).toBeNull();
  });
});
