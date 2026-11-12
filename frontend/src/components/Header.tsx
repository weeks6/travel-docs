"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Suspense, useState } from "react";
import { logout } from "@/lib/api";
import { useSession } from "@/lib/session";
import { LoginDialog } from "./account/LoginDialog";
import { AccountDialog } from "./account/AccountDialog";
import { ActivityDialog } from "./account/ActivityDialog";

const links = [
  { href: "/", label: "Routes" },
  { href: "/imports", label: "Imports" },
  { href: "/trips", label: "Trips" },
];

function NavLinks() {
  const pathname = usePathname();
  return <NavLinksView active={pathname} />;
}

function NavLinksView({ active }: { active: string | null }) {
  return (
    <nav className="mainnav">
      {links.map((l) => {
        const isActive = active !== null && (l.href === "/" ? active === "/" : active.startsWith(l.href));
        return (
          <Link key={l.href} href={l.href} className={isActive ? "active" : undefined}>
            {l.label}
          </Link>
        );
      })}
    </nav>
  );
}

export function Header() {
  const { user, ready } = useSession();
  const [dialog, setDialog] = useState<null | "login" | "register" | "account" | "activity">(null);

  return (
    <header className="topbar">
      <Link href="/" className="brand">
        Travel Docs
      </Link>
      <Suspense fallback={<NavLinksView active={null} />}>
        <NavLinks />
      </Suspense>
      <div className="auth-area">
        {!ready ? null : user ? (
          <>
            <span className="who">{user.display_name}</span>
            <button type="button" onClick={() => setDialog("account")}>
              Account
            </button>
            <button type="button" onClick={() => setDialog("activity")}>
              Activity
            </button>
            <button type="button" onClick={() => logout()}>
              Log out
            </button>
          </>
        ) : (
          <>
            <button type="button" onClick={() => setDialog("login")}>
              Log in
            </button>
            <button type="button" className="primary" onClick={() => setDialog("register")}>
              Sign up
            </button>
          </>
        )}
      </div>
      {(dialog === "login" || dialog === "register") && (
        <LoginDialog key={dialog} mode={dialog} onMode={setDialog} onClose={() => setDialog(null)} />
      )}
      {dialog === "account" && user && <AccountDialog user={user} onClose={() => setDialog(null)} />}
      {dialog === "activity" && user && <ActivityDialog user={user} onClose={() => setDialog(null)} />}
    </header>
  );
}
