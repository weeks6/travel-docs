import type { Metadata } from "next";
import { Suspense } from "react";
import { TripDetails } from "@/components/trips/TripDetails";

export const metadata: Metadata = { title: "Trip" };

// The trip id is only known at request time, so the page (which reads it
// with useParams) renders under a Suspense boundary.
export default function Page() {
  return (
    <Suspense fallback={<div className="page empty">Loading trip…</div>}>
      <TripDetails />
    </Suspense>
  );
}
