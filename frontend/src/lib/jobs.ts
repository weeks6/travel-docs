"use client";

import { useCallback, useEffect, useState } from "react";
import { importsApi } from "./api";
import { useEventsConnected, useUserEvents } from "./events";
import type { ImportJob } from "./types";

export const jobFinished = (j: ImportJob) => j.status === "succeeded" || j.status === "failed" || j.status === "canceled";

export const stageLabels: Record<string, string> = {
  queued: "Waiting in queue",
  starting: "Starting",
  parsing: "Reading GPX",
  analyzing: "Computing distance and elevation",
  saving: "Saving route",
  done: "Done",
  failed: "Failed",
  canceled: "Canceled",
};

/** Newer of two versions of the same job (by revision). */
export function newerJob(cur: ImportJob, next: ImportJob): ImportJob {
  return next.revision > cur.revision ? next : cur;
}

/**
 * Follows a job's live updates from the event stream, reloading it after
 * reconnects and polling while the stream is down.
 */
export function useJob(initial: ImportJob | null) {
  const [job, setJob] = useState(initial);
  const [prevInitial, setPrevInitial] = useState(initial);
  if (initial !== prevInitial) {
    setPrevInitial(initial);
    setJob(initial);
  }
  const connected = useEventsConnected();
  const id = job?.id;
  const finished = job ? jobFinished(job) : true;

  const reload = useCallback(() => {
    if (!id) return;
    importsApi
      .get(id)
      .then((j) => setJob((cur) => (cur && cur.id === j.id ? newerJob(cur, j) : cur)))
      .catch(() => undefined);
  }, [id]);

  useUserEvents((ev) => {
    if (ev.type === "job") setJob((cur) => (cur && cur.id === ev.job.id ? newerJob(cur, ev.job) : cur));
    else if (ev.type === "connected" && !finished) reload();
  });

  useEffect(() => {
    if (finished || connected) return;
    const t = setInterval(reload, 3000);
    return () => clearInterval(t);
  }, [finished, connected, reload]);

  return [job, setJob] as const;
}
