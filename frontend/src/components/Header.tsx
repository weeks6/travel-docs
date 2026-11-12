"use client";

import { Activity, ChevronDown, Footprints, LogOut, Map as MapIcon, Mountain, UploadCloud, UserRound } from "lucide-react";
import Link from "next/link";
import { usePathname } from "next/navigation";
import { Suspense, useState } from "react";
import { logout } from "@/lib/api";
import { useSession } from "@/lib/session";
import { cn } from "@/lib/utils";
import { AccountDialog } from "./account/AccountDialog";
import { ActivityDialog } from "./account/ActivityDialog";
import { LoginDialog } from "./account/LoginDialog";
import { Avatar, AvatarFallback } from "./ui/avatar";
import { Button } from "./ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "./ui/dropdown-menu";

const links = [
  { href: "/", label: "Routes", icon: MapIcon },
  { href: "/imports", label: "Imports", icon: UploadCloud },
  { href: "/trips", label: "Trips", icon: Footprints },
];

function NavLinks() {
  const pathname = usePathname();
  return <NavLinksView active={pathname} />;
}

function NavLinksView({ active }: { active: string | null }) {
  return (
    <nav className="flex items-center gap-1 rounded-full bg-secondary/80 p-1 ring-1 ring-border/60">
      {links.map((l) => {
        const isActive = active !== null && (l.href === "/" ? active === "/" : active.startsWith(l.href));
        const Icon = l.icon;
        return (
          <Link
            key={l.href}
            href={l.href}
            aria-current={isActive ? "page" : undefined}
            className={cn(
              "flex items-center gap-1.5 rounded-full px-3 py-1.5 text-sm font-medium text-muted-foreground transition-colors hover:text-foreground",
              isActive && "bg-card text-foreground shadow-soft",
            )}
          >
            <Icon className="size-4" aria-hidden />
            <span className="max-sm:sr-only">{l.label}</span>
          </Link>
        );
      })}
    </nav>
  );
}

function initials(name: string): string {
  const parts = name.trim().split(/\s+/).filter(Boolean);
  return ((parts[0]?.[0] || "?") + (parts[1]?.[0] || "")).toUpperCase();
}

export function Header() {
  const { user, ready } = useSession();
  const [dialog, setDialog] = useState<null | "login" | "register" | "account" | "activity">(null);

  return (
    <header className="sticky top-0 z-40 border-b bg-background/85 backdrop-blur-md">
      <div className="flex h-14 items-center gap-3 px-3 sm:gap-6 sm:px-5">
        <Link href="/" className="flex items-center gap-2 font-heading text-lg font-semibold tracking-tight">
          <span className="grid size-8 place-items-center rounded-xl bg-primary text-primary-foreground shadow-soft">
            <Mountain className="size-4.5" strokeWidth={2.4} aria-hidden />
          </span>
          <span className="max-md:hidden">Travel Docs</span>
        </Link>
        <Suspense fallback={<NavLinksView active={null} />}>
          <NavLinks />
        </Suspense>
        <div className="ml-auto flex items-center gap-2">
          {!ready ? null : user ? (
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" className="h-9 gap-2 pr-2 pl-1">
                  <Avatar className="size-7">
                    <AvatarFallback className="bg-accent text-xs font-semibold text-accent-foreground">
                      {initials(user.display_name)}
                    </AvatarFallback>
                  </Avatar>
                  <span className="who max-w-40 truncate max-sm:sr-only">{user.display_name}</span>
                  <ChevronDown className="size-4 text-muted-foreground" />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-56">
                <DropdownMenuLabel className="font-normal">
                  <div className="truncate font-medium">{user.display_name}</div>
                  <div className="truncate text-xs text-muted-foreground">{user.email}</div>
                </DropdownMenuLabel>
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={() => setDialog("account")}>
                  <UserRound /> Account
                </DropdownMenuItem>
                <DropdownMenuItem onSelect={() => setDialog("activity")}>
                  <Activity /> Activity
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem onSelect={() => logout()}>
                  <LogOut /> Log out
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          ) : (
            <>
              <Button variant="ghost" onClick={() => setDialog("login")}>
                Log in
              </Button>
              <Button onClick={() => setDialog("register")}>Sign up</Button>
            </>
          )}
        </div>
      </div>
      {(dialog === "login" || dialog === "register") && (
        <LoginDialog key={dialog} mode={dialog} onMode={setDialog} onClose={() => setDialog(null)} />
      )}
      {dialog === "account" && user && <AccountDialog user={user} onClose={() => setDialog(null)} />}
      {dialog === "activity" && user && <ActivityDialog user={user} onClose={() => setDialog(null)} />}
    </header>
  );
}
