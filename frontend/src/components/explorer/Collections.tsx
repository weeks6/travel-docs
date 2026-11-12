"use client";

import { useEffect, useRef, useState } from "react";
import { ApiError, collectionsApi, routesApi } from "@/lib/api";
import { date, plural } from "@/lib/format";
import type { Collection, Page, Route, User } from "@/lib/types";
import { SharesDialog } from "../SharesDialog";
import { ConfirmButton, Field, Modal, Msg, Pager, RoleBadge, VisibilityBadge, errorMessage, useAction } from "../ui";
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
    <div className="panel">
      <section>
        <div className="row">
          <select
            value={scope}
            onChange={(e) => {
              setPage(1);
              setScope(e.target.value);
            }}
          >
            {scopes.map(([v, l]) => (
              <option key={v} value={v}>
                {l}
              </option>
            ))}
          </select>
          {user && (
            <button type="button" className="primary" onClick={() => setCreating(true)}>
              New collection
            </button>
          )}
        </div>
      </section>
      <ul className="list">
        {res?.items.map((c) => (
          <li key={c.id} onClick={() => onOpen(c)}>
            <div className="name">
              {c.name}
              {c.visibility === "public" && <span className="badge public">public</span>}
              {c.my_role !== "owner" && <RoleBadge role={c.my_role} />}
            </div>
            <div className="meta">
              {plural(c.route_count, "route")}
              {c.my_role !== "owner" && ` · by ${c.owner_name}`} · updated {date(c.updated_at)}
            </div>
          </li>
        ))}
        {res && res.items.length === 0 && <div className="empty">No collections here yet.</div>}
        {error && <div className="empty msg error">{error}</div>}
      </ul>
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
    <div className="panel">
      <section>
        <div className="collection-title">
          <button type="button" className="link" onClick={onBack}>
            ‹ All
          </button>
          <h3>{c.name}</h3>
          <VisibilityBadge visibility={c.visibility} />
        </div>
        <div className="meta">{c.my_role === "owner" ? "Your collection" : `By ${c.owner_name} · you are ${c.my_role}`}</div>
        {c.description && <p className="description">{c.description}</p>}
        <div className="actions">
          {canEdit && (
            <button type="button" onClick={() => setDialog("edit")}>
              Edit
            </button>
          )}
          {c.my_role === "owner" && (
            <button type="button" onClick={() => setDialog("share")}>
              Share
            </button>
          )}
          {c.my_role === "owner" && (
            <ConfirmButton
              label="Delete"
              className="danger"
              question={`Delete collection “${c.name}”? Its routes are kept.`}
              onConfirm={async () => {
                await collectionsApi.remove(c.id);
                onBack();
              }}
            />
          )}
        </div>
      </section>
      <ul className="list">
        {res?.items.map((r) => (
          <RouteItem key={r.id} r={r} selected={r.id === selectedId} onSelect={onSelect} />
        ))}
        {res && res.items.length === 0 && (
          <div className="empty">No routes in this collection yet. Select a route and use “Add to collection”.</div>
        )}
        {error && <div className="empty msg error">{error}</div>}
      </ul>
      <Pager res={res} onPage={setPage} />
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
        onSubmit={(e) => {
          e.preventDefault();
          const fd = Object.fromEntries(new FormData(e.currentTarget));
          run(async () => {
            onSaved(
              c
                ? await collectionsApi.update(c.id, fd)
                : await collectionsApi.create(fd),
            );
          });
        }}
      >
        <Field label="Name">
          <input name="name" defaultValue={c?.name || ""} required maxLength={200} />
        </Field>
        <Field label="Description">
          <textarea name="description" defaultValue={c?.description || ""} maxLength={5000} />
        </Field>
        {(!c || c.my_role === "owner") && (
          <Field label="Visibility">
            <select name="visibility" defaultValue={c?.visibility || "private"}>
              <option value="private">Private</option>
              <option value="public">Public</option>
            </select>
          </Field>
        )}
        <Msg msg={msg} />
        <div className="dialog-actions">
          <button type="submit" className="primary" disabled={pending}>
            {c ? "Save" : "Create"}
          </button>
        </div>
      </form>
    </Modal>
  );
}
