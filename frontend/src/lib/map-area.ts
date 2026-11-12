export type BBox = [number, number, number, number]; // minLon, minLat, maxLon, maxLat

/**
 * The view grown to the grid of map tiles at this zoom (in degrees, one
 * tile's width), plus one tile on every side, within the world.
 */
export function tileArea([minLon, minLat, maxLon, maxLat]: BBox, zoom: number): BBox {
  const step = 360 / 2 ** zoom;
  const down = (v: number) => Math.floor(v / step) * step - step;
  const up = (v: number) => Math.ceil(v / step) * step + step;
  return [Math.max(-180, down(minLon)), Math.max(-90, down(minLat)), Math.min(180, up(maxLon)), Math.min(90, up(maxLat))];
}

export function contains(outer: BBox, inner: BBox): boolean {
  return outer[0] <= inner[0] && outer[1] <= inner[1] && outer[2] >= inner[2] && outer[3] >= inner[3];
}
