import { geoPath } from "d3-geo";
import { useEffect, useMemo, useRef, useState, type KeyboardEvent, type MouseEvent, type ReactNode } from "react";
import { api } from "../api";
import { distanceKm, loadLand, projectionFor, type LatLon } from "../lib/geo";
import type { GameContent, MapContent, MapPlace } from "../types";
import { Rich, plain, type CellProps } from "./common";

const W = 520;
const H = 300;
const KEY_STEP = 8;
const MISSES_BEFORE_HELP = 2;

interface SurfaceProps {
  frame: LatLon[];
  pins: MapPlace[];
  selected?: string;
  connect?: boolean;
  label: string;
  onPin?: (p: MapPlace) => void;
  onTap?: (point: LatLon) => void;
  onInteract?: () => void;
}

// toSvg undoes the "fit inside" scaling the browser applies to the SVG.
function toSvg(e: MouseEvent<SVGSVGElement>) {
  const r = e.currentTarget.getBoundingClientRect();
  if (!r.width || !r.height) return [e.clientX, e.clientY];
  const k = Math.min(r.width / W, r.height / H);
  return [(e.clientX - r.left - (r.width - W * k) / 2) / k, (e.clientY - r.top - (r.height - H * k) / 2) / k];
}

function MapSurface({ frame, pins, selected, connect, label, onPin, onTap, onInteract }: SurfaceProps) {
  const projection = useMemo(() => projectionFor(frame, W, H), [frame]);
  const [land, setLand] = useState<GeoJSON.GeoJSON | null>(null);
  const [marker, setMarker] = useState<[number, number] | null>(null);
  useEffect(() => {
    let live = true;
    void loadLand().then((l) => { if (live) setLand(l); }).catch(() => {});
    return () => { live = false; };
  }, []);
  const landPath = useMemo(() => (land ? geoPath(projection).digits(1)(land) ?? "" : ""), [land, projection]);
  const at = (p: MapPlace) => projection([p.lon, p.lat]) ?? [0, 0];
  const route = connect && pins.length > 1 ? `M${pins.map((p) => at(p).map((n) => n.toFixed(1)).join(" ")).join(" L")}` : "";

  const tapAt = (x: number, y: number) => {
    const ll = projection.invert?.([x, y]);
    setMarker([x, y]);
    if (ll && onTap) onTap({ lon: ll[0], lat: ll[1] });
  };
  const keys = (e: KeyboardEvent<SVGSVGElement>) => {
    if (!onTap || e.target !== e.currentTarget) return;
    const [x, y] = marker ?? [W / 2, H / 2];
    const moves: Record<string, [number, number]> = { ArrowUp: [0, -KEY_STEP], ArrowDown: [0, KEY_STEP], ArrowLeft: [-KEY_STEP, 0], ArrowRight: [KEY_STEP, 0] };
    if (moves[e.key]) {
      e.preventDefault();
      onInteract?.();
      setMarker([Math.max(0, Math.min(W, x + moves[e.key][0])), Math.max(0, Math.min(H, y + moves[e.key][1]))]);
    } else if (e.key === "Enter" || e.key === " ") {
      e.preventDefault();
      tapAt(x, y);
    }
  };
  const markerLL = marker ? projection.invert?.(marker) : null;

  return (
    <svg viewBox={`0 0 ${W} ${H}`} className="map-svg" role={onTap ? "application" : "group"} aria-label={label} tabIndex={onTap ? 0 : undefined}
      onClick={onTap ? (e) => { onInteract?.(); const [x, y] = toSvg(e); tapAt(x, y); } : undefined} onKeyDown={onTap ? keys : undefined}>
      {landPath && <path className="land" d={landPath} />}
      {route && <path className="route" d={route} />}
      {pins.map((p, i) => {
        const [x, y] = at(p);
        const on = selected === p.label;
        return (
          <g key={p.label} className={on ? "pin pin-selected" : "pin"} transform={`translate(${x} ${y})`} role="button" tabIndex={0} aria-label={`Pin: ${plain(p.label)}`}
            onClick={(e) => { e.stopPropagation(); onPin?.(p); }}
            onKeyDown={(e) => { if (e.key === "Enter" || e.key === " ") { e.preventDefault(); e.stopPropagation(); onPin?.(p); } }}>
            <circle r={on ? 11 : 9} />
            <text y={3.5}>{i + 1}</text>
            {on && <text className="pin-label" x={14} y={4} style={{ textAnchor: "start" }}>{plain(p.label)}</text>}
          </g>
        );
      })}
      {marker && markerLL && (
        <g transform={`translate(${marker[0]} ${marker[1]})`} aria-label={`Marker at ${Math.abs(markerLL[1]).toFixed(0)}°${markerLL[1] >= 0 ? "N" : "S"}, ${Math.abs(markerLL[0]).toFixed(0)}°${markerLL[0] >= 0 ? "E" : "W"}`} role="img">
          <circle className="tap-marker" r={6} />
        </g>
      )}
    </svg>
  );
}

export function MapCard({ cell }: CellProps) {
  const c = cell.content as MapContent | undefined;
  const [selected, setSelected] = useState("");
  if (!c?.places) return null;
  const choose = (p: MapPlace) => {
    setSelected(p.label);
    void api.action(cell.id, "map.explored", { place: p.label }).catch(() => {});
  };
  const current = c.places.find((p) => p.label === selected);
  return (
    <div className="map-card">
      <MapSurface frame={c.places} pins={c.places} selected={selected} connect={c.connect} label={`Map: ${plain(c.title)}`} onPin={choose} />
      {current && (
        <p className="map-fact" role="status"><b><Rich text={current.label} />{current.when && <> · <Rich text={current.when} /></>}:</b> <Rich text={current.fact} /></p>
      )}
      <div className="place-chips">
        {c.places.map((p, i) => (
          <button key={p.label} className="chip" aria-pressed={selected === p.label} onClick={() => choose(p)}>
            <span className="place-num" aria-hidden="true">{i + 1}</span><Rich text={p.label} />
          </button>
        ))}
      </div>
      <p className="map-note">Positions are approximate. Outline only, with no borders.</p>
    </div>
  );
}

type Win = (a: { level: number; point: LatLon }) => void;

export function LocateGame({ game, onWin, onTouch, done }: { game: GameContent; onWin: Win; onTouch: () => void; done: ReactNode }) {
  const places = useMemo(() => game.places ?? [], [game.places]);
  const [skipped, setSkipped] = useState<number[]>([]);
  const [misses, setMisses] = useState(0);
  const [note, setNote] = useState("");
  const tried = useRef(new Set<number>());
  const level = places.findIndex((_, i) => !game.won.includes(i) && !skipped.includes(i));
  const target = places[level];
  const revealed = places.filter((_, i) => game.won.includes(i) || skipped.includes(i));

  if (!target) return <>{done}</>;

  const tap = (point: LatLon) => {
    const km = distanceKm(point, target);
    if (km <= (game.toleranceKm ?? 0)) {
      if (tried.current.has(level)) return;
      tried.current.add(level);
      setMisses(0);
      setNote(target.fact ? `Yes! ${target.fact}` : "Yes!");
      onWin({ level, point });
    } else {
      setMisses((m) => m + 1);
      setNote(`About ${Math.round(km / 10) * 10} km off. Try again.`);
    }
  };
  const showMe = () => {
    setSkipped((s) => [...s, level]);
    setMisses(0);
    setNote(`${plain(target.label)} is marked now. No star for this one.`);
  };

  return (
    <div className="map-card">
      <div className="locate-prompt">
        <span className="level-tag">{level + 1} of {places.length}</span>
        <span className="locate-target"><Rich text={target.label} /></span>
      </div>
      <MapSurface frame={places} pins={revealed} label={`Map. Find ${plain(target.label)}. Use arrow keys and Enter, or tap.`} onTap={tap} onInteract={onTouch} />
      <div className="row">
        {note && <span className="game-feedback" role="status"><Rich text={note} /></span>}
        {misses >= MISSES_BEFORE_HELP && <button onClick={showMe}>Show me</button>}
      </div>
    </div>
  );
}
