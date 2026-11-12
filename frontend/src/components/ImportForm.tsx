"use client";

import { CheckCircle2, Loader2, RotateCcw, UploadCloud, XCircle } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { importsApi, newIdempotencyKey } from "@/lib/api";
import { bytes } from "@/lib/format";
import { jobFinished, stageLabels, useJob } from "@/lib/jobs";
import type { ImportJob } from "@/lib/types";
import { cn } from "@/lib/utils";
import { Msg, ProgressBar, errorMessage, useAction } from "./common";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { NativeSelect, NativeSelectOption } from "./ui/native-select";

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
  const [fileName, setFileName] = useState("");
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
      setFileName("");
      succeededRef.current(job);
    }
  }, [job]);

  const busy = pending || (job !== null && !jobFinished(job));

  return (
    <form
      ref={formRef}
      className="grid gap-3"
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
            const res = await importsApi.upload(fd, {
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
      <label
        className={cn(
          "group relative flex cursor-pointer items-center gap-3 rounded-xl border-2 border-dashed border-input bg-card/60 px-4 py-4 transition-colors hover:border-primary/60 hover:bg-accent/40",
          busy && "pointer-events-none opacity-60",
        )}
      >
        <span className="grid size-10 shrink-0 place-items-center rounded-full bg-accent text-accent-foreground transition-transform group-hover:-translate-y-0.5">
          <UploadCloud className="size-5" />
        </span>
        <span className="min-w-0 text-sm">
          <span className="block truncate font-medium">{fileName || "Drop a GPX file here, or browse"}</span>
          <span className="block text-xs text-muted-foreground">
            Up to {MAX_IMPORT_MB} MB, processed in the background
          </span>
        </span>
        <input
          type="file"
          name="file"
          accept=".gpx,application/gpx+xml"
          required
          disabled={busy}
          className="absolute inset-0 cursor-pointer opacity-0"
          onChange={(e) => {
            setFileName(e.target.files?.[0]?.name || "");
            setKey(newIdempotencyKey());
            setMsg({ text: "", kind: "" });
          }}
        />
      </label>
      <div className="flex flex-wrap items-center gap-2">
        {!routeId && (
          <>
            <Input
              type="text"
              name="name"
              placeholder="Name (optional)"
              maxLength={200}
              disabled={busy}
              className="min-w-24 flex-1 basis-0"
            />
            <NativeSelect name="visibility" title="Visibility" disabled={busy} defaultValue="private" className="w-auto">
              <NativeSelectOption value="private">Private</NativeSelectOption>
              <NativeSelectOption value="public">Public</NativeSelectOption>
            </NativeSelect>
          </>
        )}
        <Button type="submit" disabled={busy} className={routeId ? "ml-auto" : undefined}>
          Upload
        </Button>
      </div>
      {uploadPct !== null && (
        <div className="grid gap-1.5 rounded-xl bg-muted/60 p-3 text-sm">
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
  const failed = job.status === "failed";
  const done = job.status === "succeeded";
  return (
    <div
      className={cn(
        "grid gap-2 rounded-xl border p-3 text-sm",
        done && "border-success/30 bg-success/5",
        failed && "border-destructive/30 bg-destructive/5",
      )}
    >
      <div className="flex items-center gap-2">
        {done ? (
          <CheckCircle2 className="size-4 shrink-0 text-success" />
        ) : failed ? (
          <XCircle className="size-4 shrink-0 text-destructive" />
        ) : (
          <Loader2 className={cn("size-4 shrink-0 text-primary", active && "animate-spin")} />
        )}
        <span className="min-w-0 flex-1 truncate">
          <span className="font-medium">{job.filename || "upload"}</span>
          <span className="text-muted-foreground">
            {" · "}
            {text}
            {active && ` · ${job.progress}%`}
          </span>
        </span>
        {active && !job.cancel_requested && (
          <Button
            variant="ghost"
            size="xs"
            disabled={pending}
            onClick={() => run(() => importsApi.cancel(job.id).then(() => undefined))}
          >
            Cancel
          </Button>
        )}
        {(job.status === "failed" || job.status === "canceled") && (
          <Button
            variant="outline"
            size="xs"
            disabled={pending}
            onClick={() => run(() => importsApi.retry(job.id).then(() => undefined))}
          >
            <RotateCcw /> Retry
          </Button>
        )}
      </div>
      {active && <ProgressBar value={job.progress} status={job.status} />}
      {job.error && <Msg msg={{ text: job.error, kind: failed ? "error" : "" }} />}
      <Msg msg={msg} />
    </div>
  );
}

export { errorMessage };
