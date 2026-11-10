import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { api } from "../api";
import { projectionFor } from "../lib/geo";
import { initialState } from "../state";
import type { Cell, CellType, GameContent, MapContent, SessionState } from "../types";
import { Game } from "./Game";
import { MapCard } from "./Map";

const cell = (type: CellType, content: unknown): Cell => ({
  id: "c1", type, agentInstanceId: "a", objective: "o", status: "waiting", pinned: false, busy: false,
  layout: { col: 1, row: 1, colSpan: 4, rowSpan: 1 }, content, contextVersion: 0, userRevision: 0,
});
const session = (): SessionState => ({ ...initialState(), loaded: true, live: true });
const noop = { prefill: "", clearPrefill: () => {}, discuss: () => {} };

const content: MapContent = {
  title: "Mughal cities", connect: true,
  places: [
    { label: "Agra", lat: 27.2, lon: 78, fact: "Home of the Taj Mahal.", when: "1632" },
    { label: "Delhi", lat: 28.6, lon: 77.2, fact: "A Mughal capital.", when: "" },
  ],
};

beforeEach(() => {
  vi.restoreAllMocks();
  vi.spyOn(api, "busy").mockResolvedValue(undefined);
});

describe("Map card", () => {
  it("draws a pin for each place", () => {
    render(<MapCard cell={cell("map", content)} session={session()} {...noop} />);
    expect(screen.getByRole("button", { name: "Pin: Agra" })).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Pin: Delhi" })).toBeInTheDocument();
  });
  it("shows a place's fact when tapped, and tells the other cards", async () => {
    const action = vi.spyOn(api, "action").mockResolvedValue(undefined);
    render(<MapCard cell={cell("map", content)} session={session()} {...noop} />);
    expect(screen.queryByText("Home of the Taj Mahal.")).toBeNull();
    await userEvent.click(screen.getByRole("button", { name: "Pin: Agra" }));
    expect(screen.getByText("Home of the Taj Mahal.")).toBeInTheDocument();
    expect(action).toHaveBeenCalledWith("c1", "map.explored", { place: "Agra" });
  });
  it("works from the keyboard and has a list alternative", async () => {
    vi.spyOn(api, "action").mockResolvedValue(undefined);
    render(<MapCard cell={cell("map", content)} session={session()} {...noop} />);
    screen.getByRole("button", { name: "Pin: Delhi" }).focus();
    await userEvent.keyboard("{Enter}");
    expect(screen.getByText("A Mughal capital.")).toBeInTheDocument();
    await userEvent.click(screen.getByRole("button", { name: "Agra" }));
    expect(screen.getByText("Home of the Taj Mahal.")).toBeInTheDocument();
  });
  it("labels positions as approximate", () => {
    render(<MapCard cell={cell("map", content)} session={session()} {...noop} />);
    expect(screen.getByText(/approximate/i)).toBeInTheDocument();
  });
  it("draws the land outline once it loads, with no political borders", async () => {
    const { container } = render(<MapCard cell={cell("map", content)} session={session()} {...noop} />);
    await waitFor(() => expect(container.querySelector("path.land")?.getAttribute("d")?.length ?? 0).toBeGreaterThan(100));
    expect(container.querySelector(".border")).toBeNull();
  }, 15000);
  it("joins places when the route is the point", () => {
    const { container, rerender } = render(<MapCard cell={cell("map", content)} session={session()} {...noop} />);
    expect(container.querySelector("path.route")).not.toBeNull();
    rerender(<MapCard cell={cell("map", { ...content, connect: false })} session={session()} {...noop} />);
    expect(container.querySelector("path.route")).toBeNull();
  });
});

const locate: GameContent = {
  id: "g1", kind: "locate", title: "Find it", goal: "Tap the place.", params: [], output: { label: "", expr: "", unit: "" }, levels: [], note: "", basis: "none", mode: "none", items: [], groups: [],
  places: [{ label: "Delhi", lat: 28.6, lon: 77.2, fact: "Capital.", when: "" }, { label: "Chennai", lat: 13.1, lon: 80.3, fact: "On the coast.", when: "" }],
  toleranceKm: 200, won: [], stars: 0,
};

const tap = (lat: number, lon: number) => {
  const [x, y] = projectionFor(locate.places!, 520, 300)([lon, lat])!;
  fireEvent.click(screen.getByRole("application"), { clientX: x, clientY: y });
};

describe("Game: find it on the map", () => {
  it("names the place to find and hides its pin", () => {
    render(<Game cell={cell("game", locate)} session={session()} {...noop} />);
    expect(screen.getByText("Delhi")).toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Pin: Delhi" })).toBeNull();
  });
  it("a close tap wins, sending the point so the server can check", () => {
    const action = vi.spyOn(api, "action").mockResolvedValue(undefined);
    render(<Game cell={cell("game", locate)} session={session()} {...noop} />);
    tap(28.5, 77.1);
    expect(action).toHaveBeenCalledTimes(1);
    const [, type, payload] = action.mock.calls[0];
    expect(type).toBe("game.level_won");
    expect((payload as { level: number; point: { lat: number } }).level).toBe(0);
    expect((payload as { point: { lat: number } }).point.lat).toBeCloseTo(28.5, 1);
  });
  it("a far tap says how far off, with no star", () => {
    const action = vi.spyOn(api, "action").mockResolvedValue(undefined);
    render(<Game cell={cell("game", locate)} session={session()} {...noop} />);
    tap(13.1, 80.3);
    expect(action).not.toHaveBeenCalled();
    expect(screen.getByText(/km off/)).toBeInTheDocument();
  });
  it("offers to show the place after two misses, earning no star", async () => {
    const action = vi.spyOn(api, "action").mockResolvedValue(undefined);
    render(<Game cell={cell("game", locate)} session={session()} {...noop} />);
    expect(screen.queryByRole("button", { name: /show me/i })).toBeNull();
    tap(13.1, 80.3);
    tap(13.1, 80.3);
    await userEvent.click(screen.getByRole("button", { name: /show me/i }));
    expect(screen.getByText("Chennai")).toBeInTheDocument();
    expect(action).not.toHaveBeenCalled();
  });
  it("can be played from the keyboard", async () => {
    const action = vi.spyOn(api, "action").mockResolvedValue(undefined);
    render(<Game cell={cell("game", locate)} session={session()} {...noop} />);
    const map = screen.getByRole("application");
    map.focus();
    await userEvent.keyboard("{ArrowUp}{Enter}");
    expect(screen.getByLabelText(/Marker at/)).toBeInTheDocument();
    expect(action.mock.calls.length + (screen.queryByText(/km off/) ? 1 : 0)).toBe(1);
  });
  it("continues to the next unfound place", () => {
    render(<Game cell={cell("game", { ...locate, won: [0], stars: 1 })} session={session()} {...noop} />);
    expect(screen.getByText("Chennai")).toBeInTheDocument();
  });
  it("offers a new game when finished", () => {
    render(<Game cell={cell("game", { ...locate, won: [0, 1], stars: 2 })} session={session()} {...noop} />);
    expect(screen.getByRole("button", { name: /new game/i })).toBeInTheDocument();
  });
});
