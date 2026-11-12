"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import { invitationsApi, tripsApi } from "@/lib/api";
import { useUserEvents } from "@/lib/events";
import { date, plural } from "@/lib/format";
import { useSession } from "@/lib/session";
import type { Invitation, Page, Trip } from "@/lib/types";
import { LoginPrompt } from "../account/LoginDialog";
import { Msg, Pager, errorMessage, useAction } from "../ui";
import { statusLabels, TripFormDialog } from "./TripFormDialog";

export function TripsPage() {
  const { user, ready } = useSession();
  if (!ready) return <div className="page empty">Loading…</div>;
  if (!user) {
    return (
      <div className="page">
        <h1>Trips</h1>
        <LoginPrompt text="Log in to plan group trips and share your location live with your group." />
      </div>
    );
  }
  return <Trips key={user.id} />;
}

function Trips() {
  const router = useRouter();
  const [status, setStatus] = useState("");
  const [page, setPage] = useState(1);
  const [res, setRes] = useState<Page<Trip> | null>(null);
  const [invitations, setInvitations] = useState<Invitation[]>([]);
  const [error, setError] = useState("");
  const [reloadKey, setReloadKey] = useState(0);
  const [creating, setCreating] = useState(false);
  const reload = useCallback(() => setReloadKey((k) => k + 1), []);

  useEffect(() => {
    let active = true;
    Promise.all([
      tripsApi.list({ page, page_size: 20, status }),
      invitationsApi.mine(),
    ])
      .then(([trips, inv]) => {
        if (!active) return;
        setRes(trips);
        setInvitations(inv.items);
        setError("");
      })
      .catch((e) => active && setError(errorMessage(e)));
    return () => {
      active = false;
    };
  }, [status, page, reloadKey]);

  useUserEvents((ev) => {
    if (ev.type === "invitation" || ev.type === "connected") reload();
  });

  return (
    <div className="page">
      <div className="page-head">
        <h1>Trips</h1>
        <button type="button" className="primary" onClick={() => setCreating(true)}>
          New trip
        </button>
      </div>
      <p className="meta">
        Plan a trip, invite your group, and while it runs everyone sees each other on the map in real time.
      </p>
      {error && <p className="msg error">{error}</p>}

      {invitations.length > 0 && (
        <section className="card">
          <h3>Invitations</h3>
          {invitations.map((inv) => (
            <InvitationRow key={inv.id} inv={inv} onAccepted={() => router.push(`/trips/${inv.trip_id}`)} onChanged={reload} />
          ))}
        </section>
      )}

      <div className="row toolbar">
        <select
          value={status}
          onChange={(e) => {
            setPage(1);
            setStatus(e.target.value);
          }}
        >
          <option value="">All trips</option>
          <option value="active">Live now</option>
          <option value="planned">Planned</option>
          <option value="ended">Ended</option>
        </select>
      </div>
      <ul className="list cards">
        {res?.items.map((t) => (
          <li key={t.id}>
            <Link href={`/trips/${t.id}`} className="trip-link">
              <div className="name">
                {t.name}
                <span className={"badge status-" + t.status}>{statusLabels[t.status]}</span>
                {t.my_role !== "owner" && <span className="badge role">member</span>}
              </div>
              <div className="meta">
                {plural(t.participant_count, "participant")}
                {t.my_role !== "owner" && ` · by ${t.owner_name}`}
                {t.route_name && ` · route: ${t.route_name}`}
                {" · "}
                {t.status === "active" && t.started_at
                  ? `started ${date(t.started_at)}`
                  : t.status === "ended" && t.ended_at
                    ? `ended ${date(t.ended_at)}`
                    : `created ${date(t.created_at)}`}
              </div>
            </Link>
          </li>
        ))}
        {res && res.items.length === 0 && <div className="empty">No trips here yet.</div>}
      </ul>
      <Pager res={res} onPage={setPage} />
      {creating && (
        <TripFormDialog
          trip={null}
          onClose={() => setCreating(false)}
          onSaved={(t) => {
            setCreating(false);
            router.push(`/trips/${t.id}`);
          }}
        />
      )}
    </div>
  );
}

function InvitationRow({ inv, onAccepted, onChanged }: { inv: Invitation; onAccepted: () => void; onChanged: () => void }) {
  const { pending, msg, run } = useAction();
  return (
    <div className="invitation">
      <div>
        <strong>{inv.trip_name}</strong>
        <div className="meta">
          {inv.invited_by_name || "Someone"} invited you · {date(inv.created_at)} · {statusLabels[inv.trip_status]}
        </div>
        <Msg msg={msg} />
      </div>
      <div className="actions">
        <button
          type="button"
          className="primary"
          disabled={pending}
          onClick={() =>
            run(async () => {
              await invitationsApi.accept(inv.id);
              onAccepted();
            })
          }
        >
          Join
        </button>
        <button
          type="button"
          disabled={pending}
          onClick={() =>
            run(async () => {
              await invitationsApi.decline(inv.id);
              onChanged();
            })
          }
        >
          Decline
        </button>
      </div>
    </div>
  );
}
