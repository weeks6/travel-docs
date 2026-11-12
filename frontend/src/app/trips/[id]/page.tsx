import type { Metadata } from "next";
import { Suspense } from "react";
import { PageLoading } from "@/components/common";
import { TripDetails } from "@/components/trips/TripDetails";

export const metadata: Metadata = { title: "Trip" };

// The trip id is only known at request time, so the page (which reads it
// with useParams) renders under a Suspense boundary.
export default function Page() {
  return (
    <Suspense fallback={<PageLoading label="Loading trip…" />}>
      <TripDetails />
    </Suspense>
  );
}
