"use client";

import { useEffect, useState } from "react";
import { routesApi, tripsApi, type TripFields } from "@/lib/api";
import { fromHoursMinutes, toHoursMinutes } from "@/lib/duration";
import { km } from "@/lib/format";
import type { Route, Trip } from "@/lib/types";
import { Field, Modal, Msg, errorMessage, useAction } from "../ui";

const MAX_UPLOAD_MB = 20; // POST /trips accepts files up to routes.MaxUploadSize

/** Today as YYYY-MM-DD in the browser's time zone (the latest valid trip date). */
function today(): string {
  const d = new Date();
  return new Date(d.getTime() - d.getTimezoneOffset() * 60_000).toISOString().slice(0, 10);
}

/** The local date a recording started on. */
function localDate(iso: string): string {
  const d = new Date(iso);
  return new Date(d.getTime() - d.getTimezoneOffset() * 60_000).toISOString().slice(0, 10);
}

/**
 * Logs a trip, from an uploaded GPX file or one of the routes you can see,
 * or edits a trip (trip given). routeId preselects a route.
 */
export function TripFormDialog({
  trip,
  routeId,
  onClose,
  onSaved,
}: {
  trip?: Trip;
  routeId?: string;
  onClose: () => void;
  onSaved: (t: Trip) => void;
}) {
  const { pending, msg, setMsg, run } = useAction();
  const editing = !!trip;
  const [source, setSource] = useState<"upload" | "route">(routeId ? "route" : "upload");
  const [routes, setRoutes] = useState<Route[] | null>(null);
  const [selected, setSelected] = useState(routeId || "");
  const initial = trip ? toHoursMinutes(trip.duration_s) : null;
  const [date, setDate] = useState(trip?.date || "");
  const [hours, setHours] = useState(initial ? String(initial.hours) : "");
  const [minutes, setMinutes] = useState(initial ? String(initial.minutes) : "");

  // Fill in the date and duration recorded with a route.
  const prefill = (r: Route | undefined) => {
    if (!r) return;
    if (r.started_at) setDate(localDate(r.started_at));
    if (r.duration_s) {
      const hm = toHoursMinutes(r.duration_s);
      setHours(String(hm.hours));
      setMinutes(String(hm.minutes));
    }
  };

  useEffect(() => {
    if (editing || source !== "route" || routes) return;
    let active = true;
    routesApi
      .list({ scope: "all", sort: "updated_at", page_size: 100 })
      .then((r) => {
        if (!active) return;
        setRoutes(r.items);
        if (routeId) prefill(r.items.find((x) => x.id === routeId));
      })
      .catch((e) => active && setMsg({ text: errorMessage(e), kind: "error" }));
    return () => {
      active = false;
    };
  }, [editing, source, routes, routeId, setMsg]);

  return (
    <Modal title={editing ? "Edit trip" : "Log a trip"} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const fd = new FormData(e.currentTarget);
          const duration = fromHoursMinutes(hours, minutes);
          const fields: TripFields = {
            name: String(fd.get("name") || "").trim() || undefined,
            notes: String(fd.get("notes") || "") || undefined,
            date: date || undefined,
            duration_s: duration ?? undefined,
          };
          run(async () => {
            if (Number.isNaN(duration)) throw new Error("Enter the time spent as hours and minutes (0–59).");
            if (editing) {
              if (!fields.date || !fields.duration_s) throw new Error("Enter the date and the time spent.");
              onSaved(await tripsApi.update(trip.id, { ...fields, notes: String(fd.get("notes") || "") }));
            } else if (source === "route") {
              if (!selected) throw new Error("Choose a route.");
              onSaved(await tripsApi.create(selected, fields));
            } else {
              const file = fd.get("file");
              if (!(file instanceof File) || file.size === 0) throw new Error("Choose a GPX file.");
              if (file.size > MAX_UPLOAD_MB * 1024 * 1024) throw new Error(`The file is larger than ${MAX_UPLOAD_MB} MB.`);
              const form = new FormData();
              form.set("file", file);
              for (const [k, v] of Object.entries(fields)) if (v !== undefined) form.set(k, String(v));
              onSaved(await tripsApi.upload(form));
            }
          });
        }}
      >
        {!editing && (
          <div className="row" role="radiogroup" aria-label="Route">
            <label>
              <input type="radio" checked={source === "upload"} onChange={() => setSource("upload")} /> Upload a GPX file
            </label>
            <label>
              <input type="radio" checked={source === "route"} onChange={() => setSource("route")} /> Choose one of my routes
            </label>
          </div>
        )}
        {!editing && source === "upload" && (
          <Field label="GPX file">
            <input type="file" name="file" accept=".gpx,application/gpx+xml" required />
          </Field>
        )}
        {!editing && source === "route" && (
          <Field label="Route">
            <select
              value={selected}
              required
              onChange={(e) => {
                setSelected(e.target.value);
                prefill(routes?.find((r) => r.id === e.target.value));
              }}
            >
              <option value="">{routes ? "Choose a route…" : "Loading routes…"}</option>
              {routes?.map((r) => (
                <option key={r.id} value={r.id}>
                  {r.name} ({km(r.distance_m)}){r.my_role !== "owner" ? ` by ${r.owner_name}` : ""}
                </option>
              ))}
            </select>
          </Field>
        )}
        <div className="row">
          <Field label="Date">
            <input type="date" name="date" value={date} max={today()} onChange={(e) => setDate(e.target.value)} />
          </Field>
          <Field label="Hours">
            <input type="number" name="hours" min={0} step={1} value={hours} onChange={(e) => setHours(e.target.value)} />
          </Field>
          <Field label="Minutes">
            <input type="number" name="minutes" min={0} max={59} step={1} value={minutes} onChange={(e) => setMinutes(e.target.value)} />
          </Field>
        </div>
        {!editing && (
          <p className="meta">Leave the date and time empty to use the ones recorded in the GPX file, if it has them.</p>
        )}
        <Field label="Name">
          <input
            name="name"
            defaultValue={trip?.name || ""}
            required={editing}
            maxLength={200}
            placeholder={editing ? "" : "Defaults to the route's name"}
          />
        </Field>
        <Field label="Notes">
          <textarea name="notes" defaultValue={trip?.notes || ""} maxLength={5000} placeholder="Weather, company, how it went…" />
        </Field>
        <Msg msg={msg} />
        <div className="dialog-actions">
          <button type="submit" className="primary" disabled={pending}>
            {editing ? "Save" : "Log trip"}
          </button>
        </div>
      </form>
    </Modal>
  );
}
