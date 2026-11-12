import type { Metadata } from "next";
import { Bricolage_Grotesque, Inter } from "next/font/google";
import "leaflet/dist/leaflet.css";
import "./globals.css";
import { Header } from "@/components/Header";
import { TooltipProvider } from "@/components/ui/tooltip";
import { EventsProvider } from "@/lib/events";
import { SessionProvider } from "@/lib/session";
import { cn } from "@/lib/utils";

// Self-hosted at build time (no requests to Google from the browser).
const body = Inter({ subsets: ["latin"], variable: "--font-body" });
const display = Bricolage_Grotesque({ subsets: ["latin"], variable: "--font-display" });

export const metadata: Metadata = {
  title: { default: "Travel Docs", template: "%s · Travel Docs" },
  description: "Store GPX routes, organize them in collections and keep a log of your trips.",
};

export default function RootLayout({ children }: LayoutProps<"/">) {
  return (
    <html lang="en" className={cn(body.variable, display.variable)}>
      <body className="flex h-dvh flex-col overflow-hidden">
        <SessionProvider>
          <EventsProvider>
            <TooltipProvider delayDuration={300}>
              <Header />
              <div className="min-h-0 flex-1 overflow-y-auto">{children}</div>
            </TooltipProvider>
          </EventsProvider>
        </SessionProvider>
      </body>
    </html>
  );
}
