"use client";

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
import { Pager, ProgressBar, errorMessage, useAction } from "./ui";

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
  if (!ready) return <div className="page empty">Loading…</div>;
  if (!user) {
    return (
      <div className="page">
        <h1>Imports</h1>
        <LoginPrompt text="Log in to import GPX files." />
      </div>
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
    <div className="page">
      <h1>Imports</h1>
      <p className="meta">
        GPX files are processed in the background, up to {MAX_IMPORT_MB} MB each. You can leave this page; progress
        continues and shows up here.
        {!connected && " (Live updates are reconnecting…)"}
      </p>
      <BulkUpload onQueued={reloadSoon} />
      <div className="row toolbar">
        <select
          value={filter}
          onChange={(e) => {
            setPage(1);
            setFilter(e.target.value as typeof filter);
          }}
        >
          <option value="all">All imports</option>
          <option value="active">In progress</option>
          <option value="finished">Finished</option>
        </select>
        <button type="button" onClick={() => setReloadKey((k) => k + 1)}>
          Refresh
        </button>
      </div>
      {error && <p className="msg error">{error}</p>}
      <table className="table jobs">
        <thead>
          <tr>
            <th>File</th>
            <th>Status</th>
            <th>Started</th>
            <th />
          </tr>
        </thead>
        <tbody>
          {res?.items.map((j) => (
            <JobRow key={j.id} job={j} onChanged={reloadSoon} />
          ))}
          {res && res.items.length === 0 && (
            <tr>
              <td colSpan={4} className="meta">
                No imports here.
              </td>
            </tr>
          )}
        </tbody>
      </table>
      <Pager res={res} onPage={setPage} />
    </div>
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
    <section className="card">
      <div className="row">
        <input
          type="file"
          multiple
          accept=".gpx,application/gpx+xml"
          disabled={running}
          onChange={(e) => {
            const files = Array.from(e.target.files || []);
            e.target.value = "";
            if (!files.length) return;
            const list = files.map((file) => ({ key: newIdempotencyKey(), file, progress: 0, error: "", done: false }));
            setUploads(list);
            start(list);
          }}
        />
        <select value={visibility} onChange={(e) => setVisibility(e.target.value)} disabled={running} title="Visibility">
          <option value="private">Private</option>
          <option value="public">Public</option>
        </select>
      </div>
      {uploads.map((u) => (
        <div key={u.key} className="job-status">
          <div className="row">
            <span>
              {u.file.name} · {bytes(u.file.size)} · {u.error ? "not uploaded" : u.done ? "queued" : `uploading ${u.progress}%`}
            </span>
          </div>
          {!u.error && !u.done && <ProgressBar value={u.progress} status="running" />}
          {u.error && <div className="msg error">{u.error}</div>}
        </div>
      ))}
      {!running && failed.length > 0 && (
        <div className="row">
          <button type="button" onClick={() => start(uploads)}>
            Try failed uploads again
          </button>
          <button type="button" onClick={() => setUploads([])}>
            Dismiss
          </button>
        </div>
      )}
    </section>
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
    <tr>
      <td>
        {j.filename || "upload"}
        <div className="meta">
          {bytes(j.size_bytes)} · {j.kind === "replace_geometry" ? "new version of a route" : j.name || "new route"}
        </div>
      </td>
      <td className="status-cell">
        <span className={"status " + j.status}>{status}</span>
        {active && <ProgressBar value={j.progress} status={j.status} />}
        {j.error && <div className={"meta" + (j.status === "failed" ? " error-text" : "")}>{j.error}</div>}
        {msg.text && <div className="msg error">{msg.text}</div>}
      </td>
      <td className="meta">{dateTime(j.created_at)}</td>
      <td>
        {j.status === "succeeded" && j.route_id && (
          <Link href={`/?route=${j.route_id}`} className="link">
            Open route
          </Link>
        )}
        {active && !j.cancel_requested && (
          <button type="button" className="link" disabled={pending} onClick={() => act(importsApi.cancel)}>
            Cancel
          </button>
        )}
        {(j.status === "failed" || j.status === "canceled") && (
          <button type="button" className="link" disabled={pending} onClick={() => act(importsApi.retry)}>
            Retry
          </button>
        )}
        {!active && (
          <button type="button" className="link" disabled={pending} onClick={() => act(importsApi.remove)}>
            Remove
          </button>
        )}
      </td>
    </tr>
  );
}
