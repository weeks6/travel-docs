"use client";

import { useEffect, useState } from "react";
import { routesApi, tripsApi } from "@/lib/api";
import type { Route, Trip } from "@/lib/types";
import { Field, Modal, Msg, errorMessage, useAction } from "../ui";

export const statusLabels: Record<string, string> = { planned: "Planned", active: "Live", ended: "Ended" };

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
    routesApi
      .list({ scope: "all", sort: "updated_at", page_size: 100 })
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
                ? await tripsApi.update(trip.id, body)
                : await tripsApi.create(body),
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
