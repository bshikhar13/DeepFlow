import { geoMercator, type GeoProjection } from "d3-geo";
import { feature } from "topojson-client";
import type { GeometryObject, Topology } from "topojson-specification";

const EARTH_RADIUS_KM = 6371;
const MIN_SPAN_DEGREES = 32; // about the size of India, so a map always shows coasts for context
const BLEED = 500; // the SVG letterboxes inside its card, so land must extend past the frame
const PADDING = 26;

export interface LatLon { lat: number; lon: number }

export function distanceKm(a: LatLon, b: LatLon): number {
  const rad = Math.PI / 180;
  const dLat = (b.lat - a.lat) * rad;
  const dLon = (b.lon - a.lon) * rad;
  const h = Math.sin(dLat / 2) ** 2 + Math.cos(a.lat * rad) * Math.cos(b.lat * rad) * Math.sin(dLon / 2) ** 2;
  return 2 * EARTH_RADIUS_KM * Math.asin(Math.min(1, Math.sqrt(h)));
}

// projectionFor frames a set of places, with room around them for context.
export function projectionFor(places: LatLon[], width: number, height: number): GeoProjection {
  const lat = places.reduce((s, p) => s + p.lat, 0) / places.length;
  const lon = places.reduce((s, p) => s + p.lon, 0) / places.length;
  const half = MIN_SPAN_DEGREES / 2;
  const points = [...places.map((p) => [p.lon, p.lat]), [lon - half, lat - half], [lon + half, lat + half]];
  return geoMercator()
    .fitExtent([[PADDING, PADDING], [width - PADDING, height - PADDING]], { type: "MultiPoint", coordinates: points } as GeoJSON.MultiPoint)
    .clipExtent([[-BLEED, -BLEED], [width + BLEED, height + BLEED]]);
}

let land: Promise<GeoJSON.GeoJSON> | null = null;

// loadLand fetches the bundled land outline only when a map is first shown. It
// holds coastlines and no political borders: today's borders would be wrong on
// a history map, and borders on a school map can be contested.
export function loadLand(): Promise<GeoJSON.GeoJSON> {
  land ??= import("world-atlas/land-50m.json").then((m) => {
    const topo = m.default as unknown as Topology;
    return feature(topo, topo.objects.land as GeometryObject) as GeoJSON.GeoJSON;
  });
  return land;
}
