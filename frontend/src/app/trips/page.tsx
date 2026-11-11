import type { Metadata } from "next";
import { TripsPage } from "@/components/trips/TripsPage";

export const metadata: Metadata = { title: "Trips" };

export default function Page() {
  return <TripsPage />;
}
