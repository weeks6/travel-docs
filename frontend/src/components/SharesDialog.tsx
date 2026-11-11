"use client";

import { useCallback, useEffect, useState } from "react";
import { api } from "@/lib/api";
import type { Share } from "@/lib/types";
import { Modal, Msg, errorMessage, useAction } from "./ui";

/** Lists and edits who a route or collection is shared with (owner only). */
export function SharesDialog({
  kind,
  item,
  onClose,
}: {
  kind: "route" | "collection";
  item: { id: string; name: string };
  onClose: () => void;
}) {
  const base = `/${kind === "route" ? "routes" : "collections"}/${item.id}/shares`;
  const [shares, setShares] = useState<Share[] | null>(null);
  const [loadError, setLoadError] = useState("");
  const add = useAction();
  const remove = useAction();

  const load = useCallback(
    () =>
      api<{ items: Share[] }>(base)
        .then((res) => setShares(res.items))
        .catch((e) => setLoadError(errorMessage(e))),
    [base],
  );
  useEffect(() => {
    load();
  }, [load]);

  const hint =
    kind === "route"
      ? "Editors can change the name, description and geometry, and restore versions. Only you can share or delete."
      : "Viewers see the collection and the routes their owners added to it. Editors can also add and remove routes.";

  return (
    <Modal title={`Share “${item.name}”`} onClose={onClose}>
      <h4>People with access</h4>
      {loadError && <p className="msg error">{loadError}</p>}
      <table className="table">
        <tbody>
          {shares?.map((s) => (
            <tr key={s.user_id}>
              <td>
                {s.display_name}
                <div className="meta">{s.email}</div>
              </td>
              <td>
                {s.role}{" "}
                <button
                  type="button"
                  className="link"
                  disabled={remove.pending}
                  onClick={() =>
                    remove.run(async () => {
                      await api(`${base}/${s.user_id}`, { method: "DELETE" });
                      await load();
                    })
                  }
                >
                  Remove
                </button>
              </td>
            </tr>
          ))}
          {shares?.length === 0 && (
            <tr>
              <td className="meta">Not shared with anyone yet.</td>
            </tr>
          )}
        </tbody>
      </table>
      <Msg msg={remove.msg} />
      <h4>Add someone</h4>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const form = e.currentTarget;
          const fd = Object.fromEntries(new FormData(form));
          add.run(async () => {
            await api(base, { method: "PUT", json: fd });
            form.reset();
            await load();
            return "Shared";
          });
        }}
      >
        <div className="row">
          <input type="email" name="email" placeholder="Their account email" required />
          <select name="role" defaultValue="viewer">
            <option value="viewer">Can view</option>
            <option value="editor">Can edit</option>
          </select>
          <button type="submit" className="primary" disabled={add.pending}>
            Share
          </button>
        </div>
        <Msg msg={add.msg} />
      </form>
      <p className="meta">{hint}</p>
    </Modal>
  );
}
