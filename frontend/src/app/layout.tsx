import type { Metadata } from "next";
import "leaflet/dist/leaflet.css";
import "./globals.css";
import { Header } from "@/components/Header";
import { EventsProvider } from "@/lib/events";
import { SessionProvider } from "@/lib/session";

export const metadata: Metadata = {
  title: { default: "Travel Docs", template: "%s · Travel Docs" },
  description: "Store GPX routes, organize them in collections and follow group trips live.",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html lang="en">
      <body>
        <SessionProvider>
          <EventsProvider>
            <Header />
            <div className="content">{children}</div>
          </EventsProvider>
        </SessionProvider>
      </body>
    </html>
  );
}
