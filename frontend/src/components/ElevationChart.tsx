"use client";

import { useEffect, useId, useRef, useState } from "react";
import { km } from "@/lib/format";
import type { ProfilePoint } from "@/lib/types";

const H = 140;
const P = { l: 48, r: 8, t: 8, b: 20 };

export function ElevationChart({ points }: { points: ProfilePoint[] }) {
  const ref = useRef<SVGSVGElement>(null);
  const [W, setW] = useState(600);
  useEffect(() => {
    const svg = ref.current;
    if (!svg) return;
    const ro = new ResizeObserver(() => setW(svg.clientWidth || 600));
    ro.observe(svg);
    return () => ro.disconnect();
  }, []);

  const gradient = useId();
  const text = "fill-muted-foreground text-[10px] tabular-nums";
  if (!points.length) {
    return (
      <svg ref={ref} className="h-[140px] w-full" viewBox={`0 0 ${W} ${H}`} role="img" aria-label="Elevation profile">
        <text x={W / 2} y={H / 2} textAnchor="middle" className="fill-muted-foreground text-xs">
          No elevation data in this route
        </text>
      </svg>
    );
  }
  const maxD = points[points.length - 1].distance_m || 1;
  let minE = Math.min(...points.map((p) => p.elevation_m));
  let maxE = Math.max(...points.map((p) => p.elevation_m));
  if (maxE - minE < 10) {
    minE -= 5;
    maxE += 5;
  }
  const x = (d: number) => P.l + (d / maxD) * (W - P.l - P.r);
  const y = (e: number) => P.t + (1 - (e - minE) / (maxE - minE)) * (H - P.t - P.b);
  const line = points
    .map((p, i) => `${i ? "L" : "M"}${x(p.distance_m).toFixed(1)},${y(p.elevation_m).toFixed(1)}`)
    .join("");
  return (
    <svg
      ref={ref}
      className="h-[140px] w-full overflow-visible"
      viewBox={`0 0 ${W} ${H}`}
      preserveAspectRatio="none"
      role="img"
      aria-label="Elevation profile"
    >
      <defs>
        <linearGradient id={gradient} x1="0" x2="0" y1="0" y2="1">
          <stop offset="0%" stopColor="var(--chart-1)" stopOpacity={0.35} />
          <stop offset="100%" stopColor="var(--chart-1)" stopOpacity={0.02} />
        </linearGradient>
      </defs>
      <line x1={P.l} x2={W - P.r} y1={y(maxE)} y2={y(maxE)} className="stroke-border" strokeDasharray="3 4" />
      <line x1={P.l} x2={W - P.r} y1={y(minE)} y2={y(minE)} className="stroke-border" strokeDasharray="3 4" />
      <text x={P.l - 6} y={y(maxE) + 3} textAnchor="end" className={text}>
        {Math.round(maxE)} m
      </text>
      <text x={P.l - 6} y={y(minE) + 3} textAnchor="end" className={text}>
        {Math.round(minE)} m
      </text>
      <text x={P.l} y={H - 4} className={text}>
        0
      </text>
      <text x={W - P.r} y={H - 4} textAnchor="end" className={text}>
        {km(maxD)}
      </text>
      <path d={`${line}L${x(maxD)},${H - P.b}L${x(0)},${H - P.b}Z`} fill={`url(#${gradient})`} />
      <path d={line} fill="none" stroke="var(--chart-1)" strokeWidth={2} strokeLinejoin="round" vectorEffect="non-scaling-stroke" />
    </svg>
  );
}
