"use client";

import { Eye, FileUp, FolderMinus, FolderPlus, Footprints, History, Pencil, Share2, Trash2 } from "lucide-react";
import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { ApiError, collectionsApi, routesApi } from "@/lib/api";
import { formatDuration } from "@/lib/duration";
import { date, dateTime, km, meters } from "@/lib/format";
import type { Collection, ProfilePoint, Route, RouteVersion, User } from "@/lib/types";
import { ElevationChart } from "../ElevationChart";
import { ImportForm } from "../ImportForm";
import { SharesDialog } from "../SharesDialog";
import { TripFormDialog } from "../trips/TripFormDialog";
import { cn } from "@/lib/utils";
import {
  ConfirmButton,
  DialogActions,
  Field,
  Modal,
  Msg,
  RoleBadge,
  StatGrid,
  VisibilityBadge,
  errorMessage,
  useAction,
} from "../common";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { NativeSelect, NativeSelectOption } from "../ui/native-select";
import { Textarea } from "../ui/textarea";

type Dialog = null | "edit" | "version" | "history" | "share" | "collect" | "trip";

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
  const router = useRouter();
  const r = route;
  const canEdit = r.my_role === "owner" || r.my_role === "editor";
  const isOwner = r.my_role === "owner";
  const canRemoveFromCollection =
    collection !== null && (collection.my_role === "owner" || collection.my_role === "editor" || isOwner);
  const close = () => setDialog(null);

  return (
    <div className="grid gap-4">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0">
          <h2 className="truncate text-2xl font-semibold">{r.name}</h2>
          <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-xs text-muted-foreground">
            <VisibilityBadge visibility={r.visibility} />
            {!isOwner && <RoleBadge role={"you are " + r.my_role} />}
            <span>
              by {isOwner ? "you" : r.owner_name} · v{r.version} · updated {date(r.updated_at)}
            </span>
          </div>
        </div>
        {user && (
          <Button variant="sunset" onClick={() => setDialog("trip")}>
            <Footprints data-icon="inline-start" /> Log trip
          </Button>
        )}
      </div>
      <div className="flex flex-wrap items-center gap-1 border-y py-1.5">
        {canEdit && (
          <Button variant="ghost" size="icon-sm" aria-label="Edit" title="Edit" onClick={() => setDialog("edit")}>
            <Pencil />
          </Button>
        )}
        {canEdit && (
          <Button variant="ghost" size="icon-sm" aria-label="New version" title="New version" onClick={() => setDialog("version")}>
            <FileUp />
          </Button>
        )}
          <Button variant="ghost" size="icon-sm" aria-label="History" title="History" onClick={() => setDialog("history")}>
            <History />
          </Button>
        {isOwner && (
          <Button variant="ghost" size="icon-sm" aria-label="Share" title="Share" onClick={() => setDialog("share")}>
            <Share2 />
          </Button>
        )}
        {user && (
          <Button variant="ghost" size="icon-sm" aria-label="Add to collection" title="Add to collection" onClick={() => setDialog("collect")}>
            <FolderPlus />
          </Button>
        )}
        <div className="ml-auto flex gap-1">
          {canRemoveFromCollection && (
            <ConfirmButton
              label="Remove from collection"
              variant="ghost"
              size="sm"
              icon={<FolderMinus />}
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
              variant="ghost"
              size="sm"
              destructive
              icon={<Trash2 />}
              question={`Delete “${r.name}” and its history?`}
              onConfirm={async () => {
                await routesApi.remove(r.id);
                onDeleted();
              }}
            />
          )}
        </div>
      </div>
      {r.description && <p className="text-sm text-muted-foreground">{r.description}</p>}
      <StatGrid
        className="sm:grid-cols-4"
        stats={[
          ["Distance", km(r.distance_m)],
          ["Ascent", meters(r.ascent_m)],
          ["Descent", meters(r.descent_m)],
          ...(r.duration_s ? ([["Recorded time", formatDuration(r.duration_s)]] as [string, string][]) : []),
          ["Lowest", meters(r.min_elevation_m)],
          ["Highest", meters(r.max_elevation_m)],
          ["Points", r.point_count.toLocaleString()],
        ]}
      />
      <ElevationChart points={profile} />

      {dialog === "edit" && <EditRouteDialog route={r} onClose={close} onChanged={onChanged} />}
      {dialog === "version" && (
        <Modal title="Upload new version" onClose={close}>
          <p className="text-sm text-muted-foreground">
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
      {dialog === "trip" && (
        <TripFormDialog
          routeId={r.id}
          onClose={close}
          onSaved={(t) => {
            close(); // Next keeps this page's state for when you come back
            router.push(`/trips/${t.id}`);
          }}
        />
      )}
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
        className="grid gap-4"
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
          <Input name="name" defaultValue={r.name} required maxLength={200} />
        </Field>
        <Field label="Description">
          <Textarea name="description" defaultValue={r.description} maxLength={5000} />
        </Field>
        {r.my_role === "owner" && (
          <Field label="Visibility">
            <NativeSelect name="visibility" defaultValue={r.visibility} className="w-full">
              <NativeSelectOption value="private">Private: only you and people you share with</NativeSelectOption>
              <NativeSelectOption value="public">Public: anyone with the link</NativeSelectOption>
            </NativeSelect>
          </Field>
        )}
        <Msg msg={msg} />
        <DialogActions>
          <Button type="submit" disabled={busy || pending}>
            Save
          </Button>
        </DialogActions>
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
    <Modal
      title={`History of “${r.name}”`}
      description="“Show” draws that version on the map as a dashed line."
      onClose={onClose}
      wide
    >
      <ol className="grid gap-2">
        {versions?.map((v) => (
          <li key={v.version} className="flex items-start gap-3 rounded-xl border p-3">
            <span
              className={cn(
                "grid size-9 shrink-0 place-items-center rounded-lg bg-secondary font-heading text-sm font-semibold",
                v.version === r.version && "bg-primary text-primary-foreground",
              )}
            >
              v{v.version}
            </span>
            <div className="min-w-0 flex-1">
              <div className="text-sm font-medium">
                {changeLabels[v.change] || v.change}
                {v.restored_from ? ` from v${v.restored_from}` : ""}
                {v.version === r.version && <span className="ml-2 text-xs font-normal text-primary">current</span>}
              </div>
              <div className="truncate text-xs text-muted-foreground">
                {v.name} · {km(v.distance_m)} · ↑ {meters(v.ascent_m)} · {v.point_count} points
              </div>
              <div className="text-xs text-muted-foreground">
                {dateTime(v.created_at)} · {v.created_by_name || "unknown"}
              </div>
            </div>
            <div className="flex shrink-0 gap-1">
              <Button
                variant="ghost"
                size="sm"
                onClick={() =>
                  run(async () => {
                    onPreview(await routesApi.versionGeometry(r.id, v.version));
                  })
                }
              >
                <Eye data-icon="inline-start" /> Show
              </Button>
              {canEdit && v.version !== r.version && (
                <Button variant="outline" size="sm" onClick={() => setRestoring(v.version)}>
                  Restore
                </Button>
              )}
            </div>
          </li>
        ))}
        {!versions && [0, 1].map((i) => <li key={i} className="h-16 animate-pulse rounded-xl bg-muted" />)}
      </ol>
      {restoring !== null && (
        <div className="flex flex-wrap items-center gap-2 rounded-xl bg-accent p-3 text-sm text-accent-foreground">
          <span className="flex-1">Restore version {restoring}? It will be saved as a new version.</span>
          <Button variant="ghost" size="sm" onClick={() => setRestoring(null)}>
            Cancel
          </Button>
          <Button
            size="sm"
            disabled={pending}
            onClick={() =>
              run(async () => {
                try {
                  onChanged(await routesApi.restore(r.id, restoring, r.version));
                } catch (e) {
                  if (e instanceof ApiError && e.status === 412) onChanged(null);
                  throw new Error(concurrencyMessage(e));
                }
              })
            }
          >
            Restore
          </Button>
        </div>
      )}
      <Msg msg={msg} />
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
    <Modal
      title={`Add “${r.name}” to a collection`}
      description={
        r.my_role === "owner"
          ? "Everyone who can see the collection will be able to see this route."
          : "You can add other people’s routes only while they are public."
      }
      onClose={onClose}
    >
      <form
        className="grid gap-4"
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
          <NativeSelect name="collection" defaultValue="" className="w-full">
            <NativeSelectOption value="">{options?.length ? "Choose…" : "No other collections"}</NativeSelectOption>
            {options?.map((c) => (
              <NativeSelectOption key={c.id} value={c.id}>
                {c.name}
              </NativeSelectOption>
            ))}
          </NativeSelect>
        </Field>
        <Field label="…or create a new one">
          <Input name="new_name" placeholder="New collection name" maxLength={200} />
        </Field>
        <Msg msg={msg} />
        <DialogActions>
          <Button type="submit" disabled={pending}>
            Add
          </Button>
        </DialogActions>
      </form>
    </Modal>
  );
}
