"use client";

import { useEffect, useState } from "react";
import { ApiError, collectionsApi, routesApi } from "@/lib/api";
import { date, dateTime, km, meters } from "@/lib/format";
import type { Collection, ProfilePoint, Route, RouteVersion, User } from "@/lib/types";
import { ElevationChart } from "../ElevationChart";
import { ImportForm } from "../ImportForm";
import { SharesDialog } from "../SharesDialog";
import { ConfirmButton, Field, Modal, Msg, RoleBadge, VisibilityBadge, errorMessage, useAction } from "../ui";

type Dialog = null | "edit" | "version" | "history" | "share" | "collect";

/** Details of the selected route with its actions. */
export function RouteDetails({
  route,
  profile,
  user,
  collection,
  onChanged,
  onDeleted,
  onRemovedFromCollection,
  onPreview,
}: {
  route: Route;
  profile: ProfilePoint[];
  user: User | null;
  collection: Collection | null; // the collection the route is shown in
  onChanged: (route: Route | null) => void; // null: reload it
  onDeleted: () => void;
  onRemovedFromCollection: () => void;
  onPreview: (feature: GeoJSON.Feature) => void;
}) {
  const [dialog, setDialog] = useState<Dialog>(null);
  const r = route;
  const canEdit = r.my_role === "owner" || r.my_role === "editor";
  const isOwner = r.my_role === "owner";
  const canRemoveFromCollection =
    collection !== null && (collection.my_role === "owner" || collection.my_role === "editor" || isOwner);
  const close = () => setDialog(null);

  return (
    <div className="details">
      <div className="details-head">
        <div>
          <h2>{r.name}</h2>
          <div className="meta">
            by {isOwner ? "you" : r.owner_name} · version {r.version} · updated {date(r.updated_at)}{" "}
            <VisibilityBadge visibility={r.visibility} />
            {!isOwner && <RoleBadge role={"you are " + r.my_role} />}
          </div>
        </div>
        <div className="actions">
          {canEdit && (
            <button type="button" onClick={() => setDialog("edit")}>
              Edit
            </button>
          )}
          {canEdit && (
            <button type="button" onClick={() => setDialog("version")}>
              New version
            </button>
          )}
          <button type="button" onClick={() => setDialog("history")}>
            History
          </button>
          {isOwner && (
            <button type="button" onClick={() => setDialog("share")}>
              Share
            </button>
          )}
          {user && (
            <button type="button" onClick={() => setDialog("collect")}>
              Add to collection
            </button>
          )}
          {canRemoveFromCollection && (
            <ConfirmButton
              label="Remove from collection"
              question={`Remove “${r.name}” from “${collection.name}”? The route itself is not deleted.`}
              confirmLabel="Remove"
              onConfirm={async () => {
                await collectionsApi.removeRoute(collection.id, r.id);
                onRemovedFromCollection();
              }}
            />
          )}
          {isOwner && (
            <ConfirmButton
              label="Delete"
              className="danger"
              question={`Delete “${r.name}” and its history?`}
              onConfirm={async () => {
                await routesApi.remove(r.id);
                onDeleted();
              }}
            />
          )}
        </div>
      </div>
      {r.description && <p className="description">{r.description}</p>}
      <div className="stats">
        {(
          [
            ["Distance", km(r.distance_m)],
            ["Ascent", meters(r.ascent_m)],
            ["Descent", meters(r.descent_m)],
            ["Min elevation", meters(r.min_elevation_m)],
            ["Max elevation", meters(r.max_elevation_m)],
            ["Points", r.point_count.toLocaleString()],
          ] as const
        ).map(([label, value]) => (
          <div className="stat" key={label}>
            <div className="v">{value}</div>
            <div className="l">{label}</div>
          </div>
        ))}
      </div>
      <ElevationChart points={profile} />

      {dialog === "edit" && <EditRouteDialog route={r} onClose={close} onChanged={onChanged} />}
      {dialog === "version" && (
        <Modal title="Upload new version" onClose={close}>
          <p className="meta">
            Replaces the route geometry with a new GPX file. Name and description stay the same; the current version
            stays in the history.
          </p>
          <ImportForm
            routeId={r.id}
            ifVersion={r.version}
            onSucceeded={() => {
              close();
              onChanged(null);
            }}
          />
        </Modal>
      )}
      {dialog === "history" && (
        <HistoryDialog
          route={r}
          onClose={close}
          onPreview={(f) => {
            close();
            onPreview(f);
          }}
          onChanged={(updated) => {
            close();
            onChanged(updated);
          }}
        />
      )}
      {dialog === "share" && <SharesDialog kind="route" item={r} onClose={close} />}
      {dialog === "collect" && <AddToCollectionDialog route={r} onClose={close} />}
    </div>
  );
}

// ifMatch sends the version the user saw, so a concurrent change by someone
// else is reported (412) instead of overwritten.
function concurrencyMessage(e: unknown): string {
  if (e instanceof ApiError && e.status === 412) {
    return "Someone else changed this route meanwhile. Close this dialog to see the latest version, then try again.";
  }
  return errorMessage(e);
}

function EditRouteDialog({
  route: r,
  onClose,
  onChanged,
}: {
  route: Route;
  onClose: () => void;
  onChanged: (route: Route | null) => void;
}) {
  const { pending, msg, setMsg } = useAction();
  const [busy, setBusy] = useState(false);
  return (
    <Modal title="Edit route" onClose={onClose}>
      <form
        onSubmit={async (e) => {
          e.preventDefault();
          const fd = Object.fromEntries(new FormData(e.currentTarget));
          setBusy(true);
          try {
            const updated = await routesApi.update(r.id, r.version, fd);
            onClose();
            onChanged(updated);
          } catch (err) {
            setMsg({ text: concurrencyMessage(err), kind: "error" });
            if (err instanceof ApiError && err.status === 412) onChanged(null);
          } finally {
            setBusy(false);
          }
        }}
      >
        <Field label="Name">
          <input name="name" defaultValue={r.name} required maxLength={200} />
        </Field>
        <Field label="Description">
          <textarea name="description" defaultValue={r.description} maxLength={5000} />
        </Field>
        {r.my_role === "owner" && (
          <Field label="Visibility">
            <select name="visibility" defaultValue={r.visibility}>
              <option value="private">Private — only you and people you share with</option>
              <option value="public">Public — anyone with the link</option>
            </select>
          </Field>
        )}
        <Msg msg={msg} />
        <div className="dialog-actions">
          <button type="submit" className="primary" disabled={busy || pending}>
            Save
          </button>
        </div>
      </form>
    </Modal>
  );
}

const changeLabels: Record<string, string> = {
  create: "Uploaded",
  metadata: "Edited details",
  geometry: "New geometry",
  restore: "Restored",
};

function HistoryDialog({
  route: r,
  onClose,
  onPreview,
  onChanged,
}: {
  route: Route;
  onClose: () => void;
  onPreview: (f: GeoJSON.Feature) => void;
  onChanged: (route: Route | null) => void;
}) {
  const [versions, setVersions] = useState<RouteVersion[] | null>(null);
  const [restoring, setRestoring] = useState<number | null>(null);
  const { pending, msg, setMsg, run } = useAction();
  const canEdit = r.my_role === "owner" || r.my_role === "editor";
  useEffect(() => {
    let active = true;
    routesApi
      .versions(r.id)
      .then((res) => active && setVersions(res.items))
      .catch((e) => active && setMsg({ text: errorMessage(e), kind: "error" }));
    return () => {
      active = false;
    };
  }, [r.id, setMsg]);

  return (
    <Modal title={`History of “${r.name}”`} onClose={onClose}>
      <table className="table">
        <tbody>
          {versions?.map((v) => (
            <tr key={v.version}>
              <td>
                <strong>v{v.version} </strong>
                {changeLabels[v.change] || v.change}
                {v.restored_from ? ` from v${v.restored_from}` : ""}
                <div className="meta">
                  {v.name} · {km(v.distance_m)} · ↑ {meters(v.ascent_m)} · {v.point_count} points
                </div>
                <div className="meta">
                  {dateTime(v.created_at)} · {v.created_by_name || "unknown"}
                </div>
              </td>
              <td>
                <button
                  type="button"
                  className="link"
                  onClick={() =>
                    run(async () => {
                      onPreview(await routesApi.versionGeometry(r.id, v.version));
                    })
                  }
                >
                  Show
                </button>
                {canEdit && v.version !== r.version && (
                  <button type="button" className="link" onClick={() => setRestoring(v.version)}>
                    Restore
                  </button>
                )}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
      {restoring !== null && (
        <div className="confirm">
          <span>Restore version {restoring}? It will be saved as a new version.</span>
          <button type="button" onClick={() => setRestoring(null)}>
            Cancel
          </button>
          <button
            type="button"
            className="primary"
            disabled={pending}
            onClick={() =>
              run(async () => {
                try {
                  onChanged(
                    await routesApi.restore(r.id, restoring, r.version),
                  );
                } catch (e) {
                  if (e instanceof ApiError && e.status === 412) onChanged(null);
                  throw new Error(concurrencyMessage(e));
                }
              })
            }
          >
            Restore
          </button>
        </div>
      )}
      <Msg msg={msg} />
      <p className="meta">“Show” draws that version on the map as an orange dashed line.</p>
    </Modal>
  );
}

function AddToCollectionDialog({ route: r, onClose }: { route: Route; onClose: () => void }) {
  const [options, setOptions] = useState<Collection[] | null>(null);
  const { pending, msg, setMsg, run } = useAction();
  useEffect(() => {
    let active = true;
    Promise.all([
      collectionsApi.list({ scope: "editable", page_size: 100 }),
      collectionsApi.list({ scope: "all", page_size: 100, route_id: r.id }),
    ])
      .then(([editable, already]) => {
        if (!active) return;
        const ids = new Set(already.items.map((c) => c.id));
        setOptions(editable.items.filter((c) => !ids.has(c.id)));
      })
      .catch((e) => active && setMsg({ text: errorMessage(e), kind: "error" }));
    return () => {
      active = false;
    };
  }, [r.id, setMsg]);

  return (
    <Modal title={`Add “${r.name}” to a collection`} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const fd = new FormData(e.currentTarget);
          run(async () => {
            let id = String(fd.get("collection") || "");
            const newName = String(fd.get("new_name") || "").trim();
            if (newName) {
              id = (await collectionsApi.create({ name: newName })).id;
            }
            if (!id) throw new Error("Choose a collection or enter a name for a new one.");
            await collectionsApi.addRoute(id, r.id);
            onClose();
          });
        }}
      >
        <Field label="Collection">
          <select name="collection" defaultValue="">
            <option value="">{options?.length ? "Choose…" : "No other collections"}</option>
            {options?.map((c) => (
              <option key={c.id} value={c.id}>
                {c.name}
              </option>
            ))}
          </select>
        </Field>
        <Field label="…or create a new one">
          <input name="new_name" placeholder="New collection name" maxLength={200} />
        </Field>
        <Msg msg={msg} />
        <div className="dialog-actions">
          <button type="submit" className="primary" disabled={pending}>
            Add
          </button>
        </div>
      </form>
      <p className="meta">
        {r.my_role === "owner"
          ? "Everyone who can see the collection will be able to see this route."
          : "You can add other people’s routes only while they are public."}
      </p>
    </Modal>
  );
}
