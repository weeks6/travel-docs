"use client";

import { RefreshCw, Trash2, UploadCloud } from "lucide-react";
import Link from "next/link";
import { useCallback, useEffect, useRef, useState } from "react";
import { importsApi, newIdempotencyKey } from "@/lib/api";
import { useEventsConnected, useUserEvents } from "@/lib/events";
import { bytes, dateTime } from "@/lib/format";
import { jobFinished, newerJob, stageLabels } from "@/lib/jobs";
import { useSession } from "@/lib/session";
import type { ImportJob, Page } from "@/lib/types";
import { LoginPrompt } from "./account/LoginDialog";
import { MAX_IMPORT_MB } from "./ImportForm";
import { cn } from "@/lib/utils";
import {
  EmptyState,
  ErrorText,
  PageHeader,
  PageLoading,
  PageShell,
  Pager,
  ProgressBar,
  StatusBadge,
  errorMessage,
  useAction,
} from "./common";
import { Button } from "./ui/button";
import { Card } from "./ui/card";
import { NativeSelect, NativeSelectOption } from "./ui/native-select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui/table";
import { Tabs, TabsList, TabsTrigger } from "./ui/tabs";

// Files uploaded at the same time; the server processes them in the background.
const UPLOAD_CONCURRENCY = 2;

interface PendingUpload {
  key: string; // Idempotency-Key, reused if the upload is retried
  file: File;
  progress: number;
  error: string;
  done: boolean;
}

export function ImportsPage() {
  const { user, ready } = useSession();
  if (!ready) return <PageLoading />;
  if (!user) {
    return (
      <PageShell>
        <PageHeader title="Imports" />
        <LoginPrompt text="Log in to import GPX files." />
      </PageShell>
    );
  }
  return <Imports key={user.id} />;
}

function Imports() {
  const [filter, setFilter] = useState<"all" | "active" | "finished">("all");
  const [page, setPage] = useState(1);
  const [res, setRes] = useState<Page<ImportJob> | null>(null);
  const [error, setError] = useState("");
  const [reloadKey, setReloadKey] = useState(0);
  const connected = useEventsConnected();

  useEffect(() => {
    let active = true;
    importsApi
      .list({ page, page_size: 20, status: filter === "all" ? "" : filter })
      .then((r) => {
        if (!active) return;
        setRes(r);
        setError("");
      })
      .catch((e) => active && setError(errorMessage(e)));
    return () => {
      active = false;
    };
  }, [filter, page, reloadKey]);

  // Reload soon (coalesced) when jobs appear or move between filters.
  const reloadTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const reloadSoon = useCallback(() => {
    if (reloadTimer.current) return;
    reloadTimer.current = setTimeout(() => {
      reloadTimer.current = null;
      setReloadKey((k) => k + 1);
    }, 300);
  }, []);
  useEffect(() => () => {
    if (reloadTimer.current) clearTimeout(reloadTimer.current);
  }, []);

  useUserEvents((ev) => {
    if (ev.type === "connected") {
      reloadSoon();
      return;
    }
    if (ev.type !== "job") return;
    const j = ev.job;
    const known = res?.items.some((it) => it.id === j.id) ?? false;
    if (known) {
      setRes((cur) => cur && { ...cur, items: cur.items.map((it) => (it.id === j.id ? newerJob(it, j) : it)) });
    }
    const matches = filter === "all" || (filter === "active") === !jobFinished(j);
    if ((!known && matches && page === 1) || (known && !matches)) reloadSoon();
  });

  // Without the live stream, poll while jobs are in progress.
  const anyActive = res?.items.some((j) => !jobFinished(j)) ?? false;
  useEffect(() => {
    if (connected || !anyActive) return;
    const t = setInterval(() => setReloadKey((k) => k + 1), 3000);
    return () => clearInterval(t);
  }, [connected, anyActive]);

  return (
    <PageShell>
      <PageHeader
        title="Imports"
        description={
          <>
            GPX files are processed in the background, up to {MAX_IMPORT_MB} MB each. You can leave this page;
            progress continues and shows up here.
            {!connected && " Live updates are reconnecting…"}
          </>
        }
      />
      <BulkUpload onQueued={reloadSoon} />
      <div className="flex flex-wrap items-center justify-between gap-3">
        <Tabs
          value={filter}
          onValueChange={(v) => {
            setPage(1);
            setFilter(v as typeof filter);
          }}
        >
          <TabsList>
            <TabsTrigger value="all" className="px-3">
              All
            </TabsTrigger>
            <TabsTrigger value="active" className="px-3">
              In progress
            </TabsTrigger>
            <TabsTrigger value="finished" className="px-3">
              Finished
            </TabsTrigger>
          </TabsList>
        </Tabs>
        <Button variant="ghost" size="sm" onClick={() => setReloadKey((k) => k + 1)}>
          <RefreshCw data-icon="inline-start" /> Refresh
        </Button>
      </div>
      {error && <ErrorText>{error}</ErrorText>}
      {res && res.items.length === 0 ? (
        <EmptyState icon={<UploadCloud className="size-5" />} title="No imports here">
          Drop GPX files above to import them.
        </EmptyState>
      ) : (
        <Card className="py-0">
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead className="pl-5">File</TableHead>
                <TableHead>Status</TableHead>
                <TableHead className="max-sm:hidden">Started</TableHead>
                <TableHead className="pr-5 text-right">
                  <span className="sr-only">Actions</span>
                </TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {res?.items.map((j) => (
                <JobRow key={j.id} job={j} onChanged={reloadSoon} />
              ))}
            </TableBody>
          </Table>
        </Card>
      )}
      <Pager res={res} onPage={setPage} />
    </PageShell>
  );
}

function BulkUpload({ onQueued }: { onQueued: () => void }) {
  const [uploads, setUploads] = useState<PendingUpload[]>([]);
  const [visibility, setVisibility] = useState("private");
  const [running, setRunning] = useState(false);

  const update = (key: string, patch: Partial<PendingUpload>) =>
    setUploads((cur) => cur.map((u) => (u.key === key ? { ...u, ...patch } : u)));

  const start = async (list: PendingUpload[]) => {
    setRunning(true);
    const queue = list.filter((u) => !u.done);
    const worker = async () => {
      for (let u = queue.shift(); u; u = queue.shift()) {
        const item = u;
        if (item.file.size > MAX_IMPORT_MB * 1024 * 1024) {
          update(item.key, { error: `larger than ${MAX_IMPORT_MB} MB` });
          continue;
        }
        const fd = new FormData();
        fd.set("file", item.file);
        fd.set("visibility", visibility);
        update(item.key, { error: "", progress: 0 });
        try {
          await importsApi.upload(fd, {
            headers: { "Idempotency-Key": item.key },
            onProgress: (loaded, total) => update(item.key, { progress: Math.round((100 * loaded) / total) }),
          });
          update(item.key, { done: true, progress: 100 });
          onQueued();
        } catch (e) {
          update(item.key, { error: errorMessage(e) });
        }
      }
    };
    await Promise.all(Array.from({ length: UPLOAD_CONCURRENCY }, worker));
    setRunning(false);
    // Uploaded files leave the list; failed ones stay for another try.
    setUploads((cur) => cur.filter((u) => !u.done));
  };

  const failed = uploads.filter((u) => u.error);
  return (
    <Card className="gap-3 px-5">
      <div className="flex flex-wrap items-center gap-3">
        <label
          className={cn(
            "group relative flex min-w-0 flex-1 cursor-pointer items-center gap-3 rounded-xl border-2 border-dashed border-input px-4 py-5 transition-colors hover:border-primary/60 hover:bg-accent/40",
            running && "pointer-events-none opacity-60",
          )}
        >
          <span className="grid size-11 shrink-0 place-items-center rounded-full bg-accent text-accent-foreground transition-transform group-hover:-translate-y-0.5">
            <UploadCloud className="size-5" />
          </span>
          <span className="min-w-0 text-sm">
            <span className="block font-medium">Drop GPX files here, or browse</span>
            <span className="block text-xs text-muted-foreground">Several at once is fine; each becomes a route.</span>
          </span>
          <input
            type="file"
            multiple
            accept=".gpx,application/gpx+xml"
            disabled={running}
            className="absolute inset-0 cursor-pointer opacity-0"
            onChange={(e) => {
              const files = Array.from(e.target.files || []);
              e.target.value = "";
              if (!files.length) return;
              const list = files.map((file) => ({ key: newIdempotencyKey(), file, progress: 0, error: "", done: false }));
              setUploads(list);
              start(list);
            }}
          />
        </label>
        <NativeSelect value={visibility} onChange={(e) => setVisibility(e.target.value)} disabled={running} title="Visibility">
          <NativeSelectOption value="private">Private</NativeSelectOption>
          <NativeSelectOption value="public">Public</NativeSelectOption>
        </NativeSelect>
      </div>
      {uploads.map((u) => (
        <div key={u.key} className="grid gap-1.5 rounded-xl bg-muted/60 p-3 text-sm">
          <span className="truncate">
            <span className="font-medium">{u.file.name}</span>
            <span className="text-muted-foreground">
              {" · "}
              {bytes(u.file.size)} · {u.error ? "not uploaded" : u.done ? "queued" : `uploading ${u.progress}%`}
            </span>
          </span>
          {!u.error && !u.done && <ProgressBar value={u.progress} status="running" />}
          {u.error && <ErrorText>{u.error}</ErrorText>}
        </div>
      ))}
      {!running && failed.length > 0 && (
        <div className="flex gap-2">
          <Button variant="outline" size="sm" onClick={() => start(uploads)}>
            Try failed uploads again
          </Button>
          <Button variant="ghost" size="sm" onClick={() => setUploads([])}>
            Dismiss
          </Button>
        </div>
      )}
    </Card>
  );
}

function JobRow({ job: j, onChanged }: { job: ImportJob; onChanged: () => void }) {
  const { pending, msg, run } = useAction();
  const act = (call: (id: string) => Promise<unknown>) =>
    run(async () => {
      await call(j.id);
      onChanged();
    });
  const active = !jobFinished(j);
  let status = stageLabels[j.stage] || j.stage;
  if (j.status === "queued" && j.attempts > 0) status = `Waiting to retry (attempt ${j.attempts + 1} of ${j.max_attempts})`;
  if (j.status === "running" && j.cancel_requested) status = "Canceling…";
  if (j.status === "succeeded") status = j.kind === "replace_geometry" ? "New version saved" : "Imported";
  return (
    <TableRow>
      <TableCell className="max-w-56 pl-5 whitespace-normal">
        <div className="truncate font-medium">{j.filename || "upload"}</div>
        <div className="truncate text-xs text-muted-foreground">
          {bytes(j.size_bytes)} · {j.kind === "replace_geometry" ? "new version of a route" : j.name || "new route"}
        </div>
      </TableCell>
      <TableCell className="min-w-40 whitespace-normal">
        <div className="grid max-w-64 gap-1.5">
          <StatusBadge status={j.status}>{status}</StatusBadge>
          {active && <ProgressBar value={j.progress} status={j.status} />}
          {j.error && (
            <div className={cn("text-xs", j.status === "failed" ? "text-destructive" : "text-muted-foreground")}>{j.error}</div>
          )}
          {msg.text && <div className="text-xs text-destructive">{msg.text}</div>}
        </div>
      </TableCell>
      <TableCell className="text-xs text-muted-foreground tabular-nums max-sm:hidden">{dateTime(j.created_at)}</TableCell>
      <TableCell className="pr-5 text-right">
        <div className="flex justify-end gap-1">
          {j.status === "succeeded" && j.route_id && (
            <Button asChild variant="ghost" size="sm">
              <Link href={`/?route=${j.route_id}`}>Open route</Link>
            </Button>
          )}
          {active && !j.cancel_requested && (
            <Button variant="ghost" size="sm" disabled={pending} onClick={() => act(importsApi.cancel)}>
              Cancel
            </Button>
          )}
          {(j.status === "failed" || j.status === "canceled") && (
            <Button variant="ghost" size="sm" disabled={pending} onClick={() => act(importsApi.retry)}>
              Retry
            </Button>
          )}
          {!active && (
            <Button
              variant="ghost"
              size="icon-sm"
              aria-label="Remove"
              title="Remove"
              disabled={pending}
              onClick={() => act(importsApi.remove)}
            >
              <Trash2 />
            </Button>
          )}
        </div>
      </TableCell>
    </TableRow>
  );
}
