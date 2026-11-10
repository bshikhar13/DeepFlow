import { describe, expect, it } from "vitest";
import cases from "../../../internal/expr/testdata/cases.json";
import { evalExpr, parseExpr } from "./expr";

describe("expression language conformance", () => {
  for (const c of cases.valid) {
    it(`evaluates ${c.src}`, () => {
      const got = evalExpr(c.src, c.vars as Record<string, number>);
      if (c.want === null) expect(Number.isFinite(got)).toBe(false);
      else expect(got).toBeCloseTo(c.want, 9);
    });
  }
  for (const c of cases.invalid) {
    it(`rejects ${c.src.slice(0, 24)}`, () => {
      expect(() => parseExpr(c.src, c.allowed)).toThrow();
    });
  }
  it("doesn't reach the JS environment", () => {
    expect(() => parseExpr("constructor", [])).toThrow();
    expect(() => parseExpr("__proto__", [])).toThrow();
    expect(() => parseExpr("toString(1)", [])).toThrow();
  });
});
