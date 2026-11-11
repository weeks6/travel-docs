"use client";

import Link from "next/link";
import { useRouter } from "next/navigation";
import { useCallback, useEffect, useState } from "react";
import { api } from "@/lib/api";
import { useUserEvents } from "@/lib/events";
import { date, plural } from "@/lib/format";
import { useSession } from "@/lib/session";
import type { Invitation, Page, Route, Trip } from "@/lib/types";
import { LoginPrompt } from "../Header";
import { Field, Modal, Msg, Pager, errorMessage, useAction } from "../ui";

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

export const statusLabels: Record<string, string> = { planned: "Planned", active: "Live", ended: "Ended" };

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
      api<Page<Trip>>(`/trips?page=${page}&page_size=20${status ? "&status=" + status : ""}`),
      api<{ items: Invitation[] }>("/trip-invitations"),
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
              await api(`/trip-invitations/${inv.id}/accept`, { method: "POST" });
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
              await api(`/trip-invitations/${inv.id}/decline`, { method: "POST" });
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

/** Creates a trip, or edits one (owner only). */
export function TripFormDialog({
  trip,
  onClose,
  onSaved,
}: {
  trip: Trip | null;
  onClose: () => void;
  onSaved: (t: Trip) => void;
}) {
  const { pending, msg, setMsg, run } = useAction();
  const [routes, setRoutes] = useState<Route[] | null>(null);
  useEffect(() => {
    let active = true;
    api<Page<Route>>("/routes?scope=all&sort=updated_at&page_size=100")
      .then((r) => active && setRoutes(r.items))
      .catch((e) => active && setMsg({ text: errorMessage(e), kind: "error" }));
    return () => {
      active = false;
    };
  }, [setMsg]);

  return (
    <Modal title={trip ? "Edit trip" : "New trip"} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const fd = new FormData(e.currentTarget);
          const body = {
            name: String(fd.get("name") || ""),
            description: String(fd.get("description") || ""),
            route_id: String(fd.get("route_id") || ""),
            location_retention_days: Number(fd.get("location_retention_days")),
          };
          run(async () => {
            onSaved(
              trip
                ? await api<Trip>(`/trips/${trip.id}`, { method: "PATCH", json: body })
                : await api<Trip>("/trips", { method: "POST", json: body }),
            );
          });
        }}
      >
        <Field label="Name">
          <input name="name" defaultValue={trip?.name || ""} required maxLength={200} />
        </Field>
        <Field label="Description">
          <textarea name="description" defaultValue={trip?.description || ""} maxLength={5000} />
        </Field>
        <Field label="Planned route (optional)">
          <select name="route_id" defaultValue={trip?.route_id || ""} key={routes ? "loaded" : "loading"}>
            <option value="">No route</option>
            {trip?.route_id && !routes?.some((r) => r.id === trip.route_id) && (
              <option value={trip.route_id}>{trip.route_name || "Current route"}</option>
            )}
            {routes?.map((r) => (
              <option key={r.id} value={r.id}>
                {r.name}
                {r.my_role !== "owner" ? ` (by ${r.owner_name})` : ""}
              </option>
            ))}
          </select>
        </Field>
        <Field label="Keep location history for (days)">
          <input
            type="number"
            name="location_retention_days"
            min={1}
            max={365}
            required
            defaultValue={trip?.location_retention_days ?? 30}
          />
        </Field>
        <p className="meta">
          Participants only see the planned route if they may see it (it is public, shared with them, or in a collection
          they can see). Location points older than the history period are deleted automatically.
        </p>
        <Msg msg={msg} />
        <div className="dialog-actions">
          <button type="submit" className="primary" disabled={pending}>
            {trip ? "Save" : "Create trip"}
          </button>
        </div>
      </form>
    </Modal>
  );
}
