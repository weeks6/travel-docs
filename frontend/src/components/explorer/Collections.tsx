"use client";

import { ChevronLeft, ChevronRight, FolderOpen, Globe, Pencil, Plus, Share2, Trash2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { ApiError, collectionsApi, routesApi } from "@/lib/api";
import { date, plural } from "@/lib/format";
import type { Collection, Page, Route, User } from "@/lib/types";
import { SharesDialog } from "../SharesDialog";
import {
  ConfirmButton,
  DialogActions,
  EmptyState,
  ErrorText,
  Field,
  Modal,
  Msg,
  Pager,
  RoleBadge,
  VisibilityBadge,
  errorMessage,
  useAction,
} from "../common";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { NativeSelect, NativeSelectOption } from "../ui/native-select";
import { Textarea } from "../ui/textarea";
import { RouteItem } from "./RoutesPanel";

export function CollectionsPanel({
  user,
  collection,
  onOpen,
  selectedId,
  onSelect,
  reloadKey,
}: {
  user: User | null;
  collection: Collection | null;
  onOpen: (c: Collection | null) => void;
  selectedId?: string;
  onSelect: (r: Route) => void;
  reloadKey: number;
}) {
  const [scope, setScope] = useState(user ? "mine" : "public");
  const [page, setPage] = useState(1);
  const [res, setRes] = useState<Page<Collection> | null>(null);
  const [error, setError] = useState("");
  const [creating, setCreating] = useState(false);

  useEffect(() => {
    if (collection) return;
    let active = true;
    collectionsApi
      .list({ scope, page })
      .then((r) => {
        if (!active) return;
        setRes(r);
        setError("");
      })
      .catch((e) => active && setError(errorMessage(e)));
    return () => {
      active = false;
    };
  }, [scope, page, reloadKey, collection]);

  if (collection) {
    return (
      <CollectionView
        key={collection.id}
        initial={collection}
        onBack={() => onOpen(null)}
        selectedId={selectedId}
        onSelect={onSelect}
        reloadKey={reloadKey}
      />
    );
  }

  const scopes = user
    ? [
        ["mine", "My collections"],
        ["shared", "Shared with me"],
        ["public", "Public collections"],
      ]
    : [["public", "Public collections"]];
  return (
    <div className="grid gap-4">
      <div className="flex gap-2">
        <NativeSelect
          aria-label="Show"
          value={scope}
          className="min-w-0 flex-1"
          onChange={(e) => {
            setPage(1);
            setScope(e.target.value);
          }}
        >
          {scopes.map(([v, l]) => (
            <NativeSelectOption key={v} value={v}>
              {l}
            </NativeSelectOption>
          ))}
        </NativeSelect>
        {user && (
          <Button onClick={() => setCreating(true)}>
            <Plus data-icon="inline-start" /> New collection
          </Button>
        )}
      </div>
      <ul className="grid gap-0.5">
        {res?.items.map((c) => (
          <li key={c.id}>
            <button
              type="button"
              onClick={() => onOpen(c)}
              className="group flex w-full items-start gap-3 rounded-xl px-3 py-2.5 text-left transition-colors hover:bg-muted/70"
            >
              <span className="mt-0.5 grid size-8 shrink-0 place-items-center rounded-lg bg-sunset/15 text-sunset">
                <FolderOpen className="size-4" />
              </span>
              <span className="min-w-0 flex-1">
                <span className="flex items-center gap-1.5">
                  <span className="truncate text-sm font-semibold">{c.name}</span>
                  {c.visibility === "public" && <Globe className="size-3.5 shrink-0 text-success" aria-label="public" />}
                  {c.my_role !== "owner" && <RoleBadge role={c.my_role} />}
                </span>
                <span className="mt-0.5 block text-xs text-muted-foreground">
                  {plural(c.route_count, "route")}
                  {c.my_role !== "owner" && ` · by ${c.owner_name}`} · updated {date(c.updated_at)}
                </span>
              </span>
              <ChevronRight className="mt-2 size-4 text-muted-foreground transition-transform group-hover:translate-x-0.5" />
            </button>
          </li>
        ))}
        {!res && !error && [0, 1].map((i) => <li key={i} className="h-14 animate-pulse rounded-xl bg-muted" />)}
      </ul>
      {res && res.items.length === 0 && (
        <EmptyState icon={<FolderOpen className="size-5" />} title="No collections here yet">
          {user ? "Group routes by region, season or plan." : undefined}
        </EmptyState>
      )}
      {error && <ErrorText>{error}</ErrorText>}
      <Pager res={res} onPage={setPage} />
      {creating && (
        <CollectionFormDialog
          collection={null}
          onClose={() => setCreating(false)}
          onSaved={(c) => {
            setCreating(false);
            onOpen(c);
          }}
        />
      )}
    </div>
  );
}

export function CollectionView({
  initial,
  onBack,
  selectedId,
  onSelect,
  reloadKey,
}: {
  initial: Collection;
  onBack: () => void;
  selectedId?: string;
  onSelect: (r: Route) => void;
  reloadKey: number;
}) {
  const [c, setC] = useState(initial);
  const [page, setPage] = useState(1);
  const [res, setRes] = useState<Page<Route> | null>(null);
  const [version, setVersion] = useState(0);
  const [dialog, setDialog] = useState<null | "edit" | "share">(null);
  const [error, setError] = useState("");
  const back = useRef(onBack);
  useEffect(() => {
    back.current = onBack;
  });

  useEffect(() => {
    let active = true;
    Promise.all([
      collectionsApi.get(initial.id),
      routesApi.list({ collection_id: initial.id, page, page_size: 20 }),
    ])
      .then(([col, routes]) => {
        if (!active) return;
        setC(col);
        setRes(routes);
      })
      .catch((e) => {
        if (!active) return;
        if (e instanceof ApiError && e.status === 404) back.current();
        else setError(errorMessage(e));
      });
    return () => {
      active = false;
    };
  }, [initial.id, page, reloadKey, version]);

  const canEdit = c.my_role === "owner" || c.my_role === "editor";
  return (
    <div className="grid gap-4">
      <div className="grid gap-2">
        <Button variant="ghost" size="sm" className="-ml-2 w-fit" onClick={onBack}>
          <ChevronLeft data-icon="inline-start" /> All collections
        </Button>
        <div className="flex items-start justify-between gap-2">
          <div className="min-w-0">
            <h3 className="truncate text-xl font-semibold">{c.name}</h3>
            <div className="mt-1 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
              <VisibilityBadge visibility={c.visibility} />
              {c.my_role === "owner" ? "Your collection" : `By ${c.owner_name} · you are ${c.my_role}`}
            </div>
          </div>
          <div className="flex shrink-0 gap-1">
            {canEdit && (
              <Button variant="ghost" size="icon-sm" aria-label="Edit" title="Edit" onClick={() => setDialog("edit")}>
                <Pencil />
              </Button>
            )}
            {c.my_role === "owner" && (
              <Button variant="ghost" size="icon-sm" aria-label="Share" title="Share" onClick={() => setDialog("share")}>
                <Share2 />
              </Button>
            )}
          </div>
        </div>
        {c.description && <p className="text-sm text-muted-foreground">{c.description}</p>}
      </div>
      <ul className="grid gap-0.5">
        {res?.items.map((r) => (
          <RouteItem key={r.id} r={r} selected={r.id === selectedId} onSelect={onSelect} />
        ))}
      </ul>
      {res && res.items.length === 0 && (
        <EmptyState icon={<FolderOpen className="size-5" />} title="Nothing in here yet">
          Select a route and use “Add to collection”.
        </EmptyState>
      )}
      {error && <ErrorText>{error}</ErrorText>}
      <Pager res={res} onPage={setPage} />
      {c.my_role === "owner" && (
        <div>
          <ConfirmButton
            label="Delete collection"
            variant="ghost"
            size="sm"
            destructive
            icon={<Trash2 />}
            question={`Delete collection “${c.name}”? Its routes are kept.`}
            onConfirm={async () => {
              await collectionsApi.remove(c.id);
              onBack();
            }}
          />
        </div>
      )}
      {dialog === "edit" && (
        <CollectionFormDialog
          collection={c}
          onClose={() => setDialog(null)}
          onSaved={() => {
            setDialog(null);
            setVersion((v) => v + 1);
          }}
        />
      )}
      {dialog === "share" && <SharesDialog kind="collection" item={c} onClose={() => setDialog(null)} />}
    </div>
  );
}

export function CollectionFormDialog({
  collection: c,
  onClose,
  onSaved,
}: {
  collection: Collection | null;
  onClose: () => void;
  onSaved: (c: Collection) => void;
}) {
  const { pending, msg, run } = useAction();
  return (
    <Modal title={c ? "Edit collection" : "New collection"} onClose={onClose}>
      <form
        className="grid gap-4"
        onSubmit={(e) => {
          e.preventDefault();
          const fd = Object.fromEntries(new FormData(e.currentTarget));
          run(async () => {
            onSaved(c ? await collectionsApi.update(c.id, fd) : await collectionsApi.create(fd));
          });
        }}
      >
        <Field label="Name">
          <Input name="name" defaultValue={c?.name || ""} required maxLength={200} />
        </Field>
        <Field label="Description">
          <Textarea name="description" defaultValue={c?.description || ""} maxLength={5000} />
        </Field>
        {(!c || c.my_role === "owner") && (
          <Field label="Visibility">
            <NativeSelect name="visibility" defaultValue={c?.visibility || "private"} className="w-full">
              <NativeSelectOption value="private">Private: only you and people you share with</NativeSelectOption>
              <NativeSelectOption value="public">Public: anyone can see it</NativeSelectOption>
            </NativeSelect>
          </Field>
        )}
        <Msg msg={msg} />
        <DialogActions>
          <Button type="submit" disabled={pending}>
            {c ? "Save" : "Create collection"}
          </Button>
        </DialogActions>
      </form>
    </Modal>
  );
}
