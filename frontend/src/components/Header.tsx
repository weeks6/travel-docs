"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Suspense, useCallback, useEffect, useState } from "react";
import { api, logout, setSession, setUser } from "@/lib/api";
import { useUserEvents } from "@/lib/events";
import { dateTime } from "@/lib/format";
import { useSession } from "@/lib/session";
import type { AuditEvent, Invitation, Session, User } from "@/lib/types";
import { Field, Modal, Msg, errorMessage, useAction } from "./ui";

const links = [
  { href: "/", label: "Routes" },
  { href: "/imports", label: "Imports" },
  { href: "/trips", label: "Trips" },
];

function NavLinks({ invitations }: { invitations: number }) {
  const pathname = usePathname();
  return <NavLinksView active={pathname} invitations={invitations} />;
}

function NavLinksView({ active, invitations }: { active: string | null; invitations: number }) {
  return (
    <nav className="mainnav">
      {links.map((l) => {
        const isActive = active !== null && (l.href === "/" ? active === "/" : active.startsWith(l.href));
        return (
          <Link key={l.href} href={l.href} className={isActive ? "active" : undefined}>
            {l.label}
            {l.href === "/trips" && invitations > 0 && (
              <span className="count" title="Pending trip invitations">
                {invitations}
              </span>
            )}
          </Link>
        );
      })}
    </nav>
  );
}

export function Header() {
  const { user, ready } = useSession();
  const [dialog, setDialog] = useState<null | "login" | "register" | "account" | "activity">(null);
  const [invitations, setInvitations] = useState(0);

  const loadInvitations = useCallback(() => {
    api<{ items: Invitation[] }>("/trip-invitations")
      .then((res) => setInvitations(res.items.length))
      .catch(() => undefined);
  }, []);
  useEffect(() => {
    if (user) loadInvitations();
  }, [user, loadInvitations]);
  useUserEvents((ev) => {
    if (ev.type === "invitation" || ev.type === "connected") loadInvitations();
  });

  return (
    <header className="topbar">
      <Link href="/" className="brand">
        Travel Docs
      </Link>
      <Suspense fallback={<NavLinksView active={null} invitations={0} />}>
        <NavLinks invitations={user ? invitations : 0} />
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

/** Opens the login dialog from anywhere (e.g. "Log in to upload"). */
export function LoginPrompt({ text }: { text: string }) {
  const [mode, setMode] = useState<null | "login" | "register">(null);
  return (
    <div className="empty">
      {text}{" "}
      <button type="button" className="link" onClick={() => setMode("login")}>
        Log in
      </button>{" "}
      or{" "}
      <button type="button" className="link" onClick={() => setMode("register")}>
        sign up
      </button>
      .
      {mode && <LoginDialog key={mode} mode={mode} onMode={setMode} onClose={() => setMode(null)} />}
    </div>
  );
}

function LoginDialog({
  mode,
  onMode,
  onClose,
}: {
  mode: "login" | "register";
  onMode: (m: "login" | "register") => void;
  onClose: () => void;
}) {
  const register = mode === "register";
  const { pending, msg, run } = useAction();
  return (
    <Modal title={register ? "Create account" : "Log in"} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const fd = Object.fromEntries(new FormData(e.currentTarget));
          run(async () => {
            const s = await api<Session>(register ? "/auth/register" : "/auth/login", { method: "POST", json: fd });
            setSession(s);
            onClose();
          });
        }}
      >
        <Field label="Email">
          <input type="email" name="email" required autoComplete="email" />
        </Field>
        {register && (
          <Field label="Display name">
            <input name="display_name" maxLength={100} placeholder="Optional" />
          </Field>
        )}
        <Field label="Password">
          <input
            type="password"
            name="password"
            required
            minLength={register ? 8 : undefined}
            autoComplete={register ? "new-password" : "current-password"}
          />
        </Field>
        <Msg msg={msg} />
        <div className="dialog-actions">
          <button type="submit" className="primary" disabled={pending}>
            {register ? "Create account" : "Log in"}
          </button>
        </div>
      </form>
      <button type="button" className="link" onClick={() => onMode(register ? "login" : "register")}>
        {register ? "Already have an account? Log in" : "No account yet? Sign up"}
      </button>
    </Modal>
  );
}

function AccountDialog({ user, onClose }: { user: User; onClose: () => void }) {
  const profile = useAction();
  const password = useAction();
  const sessions = useAction();
  const del = useAction();
  const [confirmDelete, setConfirmDelete] = useState(false);
  return (
    <Modal title="Account" onClose={onClose}>
      <h4>Profile</h4>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const fd = Object.fromEntries(new FormData(e.currentTarget));
          profile.run(async () => {
            setUser(await api<User>("/auth/me", { method: "PATCH", json: fd }));
            return "Saved";
          });
        }}
      >
        <Field label="Display name">
          <input name="display_name" defaultValue={user.display_name} required maxLength={100} />
        </Field>
        <Field label="Email">
          <input type="email" name="email" defaultValue={user.email} required />
        </Field>
        <Field label="Current password (needed to change email)">
          <input type="password" name="current_password" autoComplete="current-password" />
        </Field>
        <Msg msg={profile.msg} />
        <div className="dialog-actions">
          <button type="submit" className="primary" disabled={profile.pending}>
            Save profile
          </button>
        </div>
      </form>

      <h4>Password</h4>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const form = e.currentTarget;
          const fd = Object.fromEntries(new FormData(form));
          password.run(async () => {
            setSession(await api<Session>("/auth/me/password", { method: "PUT", json: fd }));
            form.reset();
            return "Password changed. Other devices were logged out.";
          });
        }}
      >
        <Field label="Current password">
          <input type="password" name="current_password" required autoComplete="current-password" />
        </Field>
        <Field label="New password">
          <input type="password" name="new_password" required minLength={8} autoComplete="new-password" />
        </Field>
        <Msg msg={password.msg} />
        <div className="dialog-actions">
          <button type="submit" className="primary" disabled={password.pending}>
            Change password
          </button>
        </div>
      </form>

      <h4>Sessions</h4>
      <Msg msg={sessions.msg} />
      <div className="dialog-actions">
        <button
          type="button"
          disabled={sessions.pending}
          onClick={() =>
            sessions.run(async () => {
              await api("/auth/logout-all", { method: "POST" });
              setSession(null);
              onClose();
            })
          }
        >
          Log out everywhere (including here)
        </button>
      </div>

      <h4>Delete account</h4>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!confirmDelete) {
            setConfirmDelete(true);
            return;
          }
          const fd = Object.fromEntries(new FormData(e.currentTarget));
          del.run(async () => {
            await api("/auth/me", { method: "DELETE", json: fd });
            setSession(null);
            onClose();
          });
        }}
      >
        <Field label="Password">
          <input type="password" name="password" required autoComplete="current-password" />
        </Field>
        {confirmDelete && (
          <p className="msg error">
            This deletes your account with all your routes, collections and trips. It cannot be undone. Press the
            button again to confirm.
          </p>
        )}
        <Msg msg={del.msg} />
        <div className="dialog-actions">
          <button type="submit" className="danger" disabled={del.pending}>
            {confirmDelete ? "Yes, delete everything" : "Delete my account"}
          </button>
        </div>
      </form>
    </Modal>
  );
}

const actionLabels: Record<string, string> = {
  "user.register": "Created account",
  "user.login": "Logged in",
  "user.login_failed": "Failed login attempt",
  "user.logout": "Logged out",
  "user.logout_all": "Logged out everywhere",
  "user.update": "Updated profile",
  "user.password_change": "Changed password",
  "user.refresh_token_reuse": "Session ended: reused token detected",
  "route.create": "Uploaded route",
  "route.update": "Edited route",
  "route.geometry_update": "Uploaded new route version",
  "route.restore": "Restored route version",
  "route.visibility_change": "Changed route visibility",
  "route.delete": "Deleted route",
  "route.share": "Shared route",
  "route.unshare": "Removed route share",
  "collection.create": "Created collection",
  "collection.update": "Edited collection",
  "collection.delete": "Deleted collection",
  "collection.share": "Shared collection",
  "collection.unshare": "Removed collection share",
  "collection.route_add": "Added route to collection",
  "collection.route_remove": "Removed route from collection",
  "trip.create": "Created trip",
  "trip.update": "Edited trip",
  "trip.delete": "Deleted trip",
  "trip.start": "Started trip",
  "trip.end": "Ended trip",
  "trip.invite": "Invited someone to a trip",
  "trip.invite_revoke": "Withdrew a trip invitation",
  "trip.invite_accept": "Joined a trip",
  "trip.invite_decline": "Declined a trip invitation",
  "trip.participant_remove": "Removed a trip participant",
  "trip.leave": "Left a trip",
};

function eventDetail(ev: AuditEvent): string {
  const d = ev.details || {};
  const parts: string[] = [];
  if (typeof d.name === "string") parts.push(d.name);
  if (typeof d.email === "string") parts.push(d.email);
  if (Array.isArray(d.fields)) parts.push(d.fields.join(", "));
  if (typeof d.to === "string") parts.push(`${d.from} → ${d.to}`);
  if (typeof d.import_job_id === "string") parts.push("background import");
  return parts.join(" · ");
}

function ActivityDialog({ user, onClose }: { user: User; onClose: () => void }) {
  const [items, setItems] = useState<AuditEvent[]>([]);
  const [next, setNext] = useState<number | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");

  useEffect(() => {
    let active = true;
    fetchActivity(0)
      .then((res) => {
        if (!active) return;
        setItems(res.items);
        setNext(res.next_before_id ?? null);
      })
      .catch((e) => active && setError(errorMessage(e)))
      .finally(() => active && setLoading(false));
    return () => {
      active = false;
    };
  }, []);
  const loadMore = () => {
    if (next === null) return;
    setLoading(true);
    fetchActivity(next)
      .then((res) => {
        setItems((cur) => [...cur, ...res.items]);
        setNext(res.next_before_id ?? null);
      })
      .catch((e) => setError(errorMessage(e)))
      .finally(() => setLoading(false));
  };

  return (
    <Modal title="Activity" onClose={onClose}>
      <table className="table">
        <tbody>
          {items.map((ev) => (
            <tr key={ev.id}>
              <td>
                {actionLabels[ev.action] || ev.action}
                {eventDetail(ev) && <div className="meta">{eventDetail(ev)}</div>}
                {ev.actor_id && ev.actor_id !== user.id && (
                  <div className="meta">by {ev.actor_name || "deleted user"}</div>
                )}
              </td>
              <td className="meta">{dateTime(ev.occurred_at)}</td>
            </tr>
          ))}
        </tbody>
      </table>
      {loading && <p className="meta">Loading…</p>}
      {error && <p className="msg error">{error}</p>}
      {next !== null && !loading && (
        <div className="dialog-actions">
          <button type="button" onClick={loadMore}>
            Load more
          </button>
        </div>
      )}
    </Modal>
  );
}

function fetchActivity(before: number) {
  return api<{ items: AuditEvent[]; next_before_id?: number }>(
    "/audit-events?limit=30" + (before ? "&before_id=" + before : ""),
  );
}
