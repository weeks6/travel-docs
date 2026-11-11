import type { Metadata } from "next";
import { Suspense } from "react";
import { TripView } from "@/components/trips/TripView";

export const metadata: Metadata = { title: "Trip" };

// The trip id is only known at request time, so the view (which reads it
// with useParams) renders under a Suspense boundary.
export default function Page() {
  return (
    <Suspense fallback={<div className="page empty">Loading trip…</div>}>
      <TripView />
    </Suspense>
  );
}
