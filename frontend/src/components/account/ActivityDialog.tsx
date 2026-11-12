"use client";

import { useEffect, useState } from "react";
import { auditApi } from "@/lib/api";
import { dateTime } from "@/lib/format";
import type { AuditEvent, User } from "@/lib/types";
import { cn } from "@/lib/utils";
import { DialogActions, ErrorText, Modal, errorMessage } from "../common";
import { Button } from "../ui/button";
import { Skeleton } from "../ui/skeleton";

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
  "trip.create": "Logged a trip",
  "trip.update": "Edited a trip",
  "trip.delete": "Deleted a trip",
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
    <Modal title="Activity" description="What happened in your account, newest first." onClose={onClose}>
      <ol className="relative grid gap-4 border-l pl-5">
        {items.map((ev) => (
          <li key={ev.id} className="relative">
            <span
              aria-hidden
              className={cn(
                "absolute top-1.5 -left-[25px] size-2.5 rounded-full ring-4 ring-popover",
                ev.action.includes("fail") || ev.action.includes("reuse") ? "bg-destructive" : "bg-primary",
              )}
            />
            <div className="flex flex-wrap items-baseline justify-between gap-x-3">
              <span className="text-sm font-medium">{actionLabels[ev.action] || ev.action}</span>
              <time className="text-xs text-muted-foreground tabular-nums">{dateTime(ev.occurred_at)}</time>
            </div>
            {eventDetail(ev) && <div className="text-xs text-muted-foreground">{eventDetail(ev)}</div>}
            {ev.actor_id && ev.actor_id !== user.id && (
              <div className="text-xs text-muted-foreground">by {ev.actor_name || "deleted user"}</div>
            )}
          </li>
        ))}
        {loading && (
          <li className="grid gap-2">
            <Skeleton className="h-4 w-2/3" />
            <Skeleton className="h-3 w-1/3" />
          </li>
        )}
      </ol>
      {error && <ErrorText>{error}</ErrorText>}
      {next !== null && !loading && (
        <DialogActions>
          <Button variant="outline" onClick={loadMore}>
            Load more
          </Button>
        </DialogActions>
      )}
    </Modal>
  );
}

export function fetchActivity(before: number) {
  return auditApi.list(before);
}
