"use client";

import { useEffect, useState } from "react";
import { auditApi } from "@/lib/api";
import { dateTime } from "@/lib/format";
import type { AuditEvent, User } from "@/lib/types";
import { Modal, errorMessage } from "../ui";

export const actionLabels: Record<string, string> = {
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

export function eventDetail(ev: AuditEvent): string {
  const d = ev.details || {};
  const parts: string[] = [];
  if (typeof d.name === "string") parts.push(d.name);
  if (typeof d.email === "string") parts.push(d.email);
  if (Array.isArray(d.fields)) parts.push(d.fields.join(", "));
  if (typeof d.to === "string") parts.push(`${d.from} → ${d.to}`);
  if (typeof d.import_job_id === "string") parts.push("background import");
  return parts.join(" · ");
}

export function ActivityDialog({ user, onClose }: { user: User; onClose: () => void }) {
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

export function fetchActivity(before: number) {
  return auditApi.list(before);
}
