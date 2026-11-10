import { describe, expect, it } from "vitest";
import { distanceKm, projectionFor } from "./geo";

const places = [{ lat: 28.6, lon: 77.2 }, { lat: 13.1, lon: 80.3 }];

describe("map geometry", () => {
  it("clips drawing to the view, so regional maps stay light", () => {
    const p = projectionFor(places, 520, 300);
    const [[x0], [x1]] = p.clipExtent()!;
    expect(x0).toBeLessThan(-200); // the SVG letterboxes, so land must extend past the view
    expect(x1).toBeGreaterThan(520 + 200);
  });
  it("measures distance the same way as the server", () => {
    const d = distanceKm({ lat: 28.6, lon: 77.2 }, { lat: 19.1, lon: 72.9 });
    expect(d).toBeGreaterThan(1100);
    expect(d).toBeLessThan(1200);
    expect(distanceKm({ lat: 0, lon: 179 }, { lat: 0, lon: -179 })).toBeLessThan(300);
  });
  it("fits every place inside the view", () => {
    const p = projectionFor(places, 520, 300);
    for (const pl of places) {
      const [x, y] = p([pl.lon, pl.lat])!;
      expect(x).toBeGreaterThan(0);
      expect(x).toBeLessThan(520);
      expect(y).toBeGreaterThan(0);
      expect(y).toBeLessThan(300);
    }
  });
  it("turns a tap back into coordinates", () => {
    const p = projectionFor(places, 520, 300);
    const [x, y] = p([77.2, 28.6])!;
    const [lon, lat] = p.invert!([x, y])!;
    expect(lat).toBeCloseTo(28.6, 3);
    expect(lon).toBeCloseTo(77.2, 3);
  });
  it("doesn't zoom absurdly close on neighbouring places", () => {
    const a = projectionFor([{ lat: 27.17, lon: 78.04 }, { lat: 27.18, lon: 78.05 }], 520, 300);
    const [x1] = a([78.04, 27.17])!;
    const [x2] = a([79.04, 27.17])!;
    expect(Math.abs(x2 - x1)).toBeLessThan(25); // wide enough to show coasts for context
  });
});
