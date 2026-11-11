"use client";

import { useEffect, useRef, useState } from "react";
import { api, newIdempotencyKey, upload } from "@/lib/api";
import { bytes } from "@/lib/format";
import { jobFinished, stageLabels, useJob } from "@/lib/jobs";
import type { ImportJob } from "@/lib/types";
import { Msg, ProgressBar, errorMessage, useAction } from "./ui";

export const MAX_IMPORT_MB = 50;

/**
 * Uploads a GPX file as a background import job and follows it until the
 * route is ready. With routeId, the file becomes a new version of that route.
 */
export function ImportForm({
  routeId,
  ifVersion,
  onSucceeded,
}: {
  routeId?: string;
  ifVersion?: number;
  onSucceeded: (job: ImportJob) => void;
}) {
  const formRef = useRef<HTMLFormElement>(null);
  // One key per chosen file: resubmitting after a network error can't import twice.
  const [key, setKey] = useState(newIdempotencyKey);
  const [uploaded, setUploaded] = useState<ImportJob | null>(null);
  const [uploadPct, setUploadPct] = useState<number | null>(null);
  const [replayed, setReplayed] = useState(false);
  const [job] = useJob(uploaded);
  const { pending, msg, setMsg, run } = useAction();

  const notified = useRef<string | null>(null);
  const succeededRef = useRef(onSucceeded);
  useEffect(() => {
    succeededRef.current = onSucceeded;
  });
  useEffect(() => {
    if (job && job.status === "succeeded" && notified.current !== job.id) {
      notified.current = job.id;
      formRef.current?.reset();
      succeededRef.current(job);
    }
  }, [job]);

  const busy = pending || (job !== null && !jobFinished(job));

  return (
    <form
      ref={formRef}
      onSubmit={(e) => {
        e.preventDefault();
        const fd = new FormData(e.currentTarget);
        const file = fd.get("file");
        if (!(file instanceof File) || !file.name) return;
        if (file.size > MAX_IMPORT_MB * 1024 * 1024) {
          setMsg({ text: `The file is ${bytes(file.size)}; the limit is ${MAX_IMPORT_MB} MB.`, kind: "error" });
          return;
        }
        if (routeId) fd.set("route_id", routeId);
        const headers: Record<string, string> = { "Idempotency-Key": key };
        if (routeId && ifVersion) headers["If-Match"] = `"${ifVersion}"`;
        run(async () => {
          setUploadPct(0);
          try {
            const res = await upload<ImportJob>("/import-jobs", fd, {
              headers,
              onProgress: (loaded, total) => setUploadPct(Math.round((100 * loaded) / total)),
            });
            setReplayed(res.replayed);
            setUploaded(res.data);
            setKey(newIdempotencyKey());
          } finally {
            setUploadPct(null);
          }
        });
      }}
    >
      <div className="row">
        <input
          type="file"
          name="file"
          accept=".gpx,application/gpx+xml"
          required
          disabled={busy}
          onChange={() => {
            setKey(newIdempotencyKey());
            setMsg({ text: "", kind: "" });
          }}
        />
      </div>
      {!routeId && (
        <div className="row">
          <input type="text" name="name" placeholder="Name (optional)" maxLength={200} disabled={busy} />
          <select name="visibility" title="Visibility" disabled={busy} defaultValue="private">
            <option value="private">Private</option>
            <option value="public">Public</option>
          </select>
        </div>
      )}
      <div className="row">
        <span className="meta">GPX up to {MAX_IMPORT_MB} MB, processed in the background.</span>
        <button className="primary" type="submit" disabled={busy}>
          Upload
        </button>
      </div>
      {uploadPct !== null && (
        <div className="job-status">
          <span>Uploading… {uploadPct}%</span>
          <ProgressBar value={uploadPct} status="running" />
        </div>
      )}
      {job && uploadPct === null && <JobStatus job={job} replayed={replayed} />}
      <Msg msg={msg} />
    </form>
  );
}

/** One job's progress with cancel and retry. */
export function JobStatus({ job, replayed }: { job: ImportJob; replayed?: boolean }) {
  const { pending, msg, run } = useAction();
  const active = !jobFinished(job);
  let text = stageLabels[job.stage] || job.stage;
  if (job.status === "queued" && job.attempts > 0) text = `Retrying (attempt ${job.attempts + 1} of ${job.max_attempts})`;
  if (job.status === "running" && job.cancel_requested) text = "Canceling…";
  if (job.status === "succeeded") text = replayed ? "This upload was already imported" : "Imported";
  return (
    <div className="job-status">
      <div className="row">
        <span>
          {job.filename || "upload"} · {text}
          {active && ` · ${job.progress}%`}
        </span>
        {active && !job.cancel_requested && (
          <button
            type="button"
            className="link"
            disabled={pending}
            onClick={() => run(() => api(`/import-jobs/${job.id}/cancel`, { method: "POST" }).then(() => undefined))}
          >
            Cancel
          </button>
        )}
        {(job.status === "failed" || job.status === "canceled") && (
          <button
            type="button"
            className="link"
            disabled={pending}
            onClick={() => run(() => api(`/import-jobs/${job.id}/retry`, { method: "POST" }).then(() => undefined))}
          >
            Retry
          </button>
        )}
      </div>
      {active && <ProgressBar value={job.progress} status={job.status} />}
      {job.error && <div className={"msg" + (job.status === "failed" ? " error" : "")}>{job.error}</div>}
      <Msg msg={msg} />
    </div>
  );
}

export { errorMessage };
