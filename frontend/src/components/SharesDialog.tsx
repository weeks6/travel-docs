"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { sharesApi } from "@/lib/api";
import type { Share } from "@/lib/types";
import { X } from "lucide-react";
import { ErrorText, Modal, Msg, errorMessage, useAction } from "./common";
import { Avatar, AvatarFallback } from "./ui/avatar";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { NativeSelect, NativeSelectOption } from "./ui/native-select";

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
  const endpoint = useMemo(() => sharesApi(kind, item.id), [kind, item.id]);
  const [shares, setShares] = useState<Share[] | null>(null);
  const [loadError, setLoadError] = useState("");
  const add = useAction();
  const remove = useAction();

  const load = useCallback(
    () =>
      endpoint
        .list()
        .then((res) => setShares(res.items))
        .catch((e) => setLoadError(errorMessage(e))),
    [endpoint],
  );
  useEffect(() => {
    load();
  }, [load]);

  const hint =
    kind === "route"
      ? "Editors can change the name, description and geometry, and restore versions. Only you can share or delete."
      : "Viewers see the collection and the routes their owners added to it. Editors can also add and remove routes.";

  return (
    <Modal title={`Share “${item.name}”`} description={hint} onClose={onClose}>
      <form
        className="grid gap-2"
        onSubmit={(e) => {
          e.preventDefault();
          const form = e.currentTarget;
          const fd = Object.fromEntries(new FormData(form));
          add.run(async () => {
            await endpoint.put(fd);
            form.reset();
            await load();
            return "Shared";
          });
        }}
      >
        <div className="flex gap-2">
          <Input type="email" name="email" placeholder="Their account email" required aria-label="Email" className="flex-1" />
          <NativeSelect name="role" defaultValue="viewer" aria-label="Role">
            <NativeSelectOption value="viewer">Can view</NativeSelectOption>
            <NativeSelectOption value="editor">Can edit</NativeSelectOption>
          </NativeSelect>
          <Button type="submit" disabled={add.pending}>
            Share
          </Button>
        </div>
        <Msg msg={add.msg} />
      </form>
      <div className="grid gap-1">
        <h4 className="text-xs font-semibold tracking-wide text-muted-foreground uppercase">People with access</h4>
        {loadError && <ErrorText>{loadError}</ErrorText>}
        <ul className="divide-y">
          {shares?.map((s) => (
            <li key={s.user_id} className="flex items-center gap-3 py-2.5">
              <Avatar className="size-8">
                <AvatarFallback className="bg-accent text-xs font-semibold text-accent-foreground">
                  {(s.display_name || s.email).slice(0, 1).toUpperCase()}
                </AvatarFallback>
              </Avatar>
              <div className="min-w-0 flex-1">
                <div className="truncate text-sm font-medium">{s.display_name}</div>
                <div className="truncate text-xs text-muted-foreground">{s.email}</div>
              </div>
              <Badge variant="secondary">{s.role === "editor" ? "Can edit" : "Can view"}</Badge>
              <Button
                variant="ghost"
                size="icon-sm"
                aria-label={`Remove ${s.display_name}`}
                title="Remove"
                disabled={remove.pending}
                onClick={() =>
                  remove.run(async () => {
                    await endpoint.remove(s.user_id);
                    await load();
                  })
                }
              >
                <X />
              </Button>
            </li>
          ))}
          {shares?.length === 0 && <li className="py-3 text-sm text-muted-foreground">Not shared with anyone yet.</li>}
        </ul>
        <Msg msg={remove.msg} />
      </div>
    </Modal>
  );
}
