"use client";

// The app's shared building blocks, on top of the shadcn/ui primitives in
// ./ui: dialogs, form fields, messages, paging, badges and stats.

import { AlertCircle, CheckCircle2, ChevronLeft, ChevronRight, Globe, Lock } from "lucide-react";
import { useState } from "react";
import { ApiError } from "@/lib/api";
import type { Page } from "@/lib/types";
import { cn } from "@/lib/utils";
import {
  AlertDialog,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "./ui/alert-dialog";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "./ui/dialog";
import { Progress } from "./ui/progress";

export function errorMessage(e: unknown): string {
  if (e instanceof ApiError) return e.message;
  if (e instanceof Error) return e.message;
  return String(e);
}

/** A modal dialog, open while rendered. */
export function Modal({
  title,
  description,
  onClose,
  children,
  wide,
}: {
  title: string;
  description?: string;
  onClose: () => void;
  children: React.ReactNode;
  wide?: boolean;
}) {
  return (
    <Dialog open onOpenChange={(open) => !open && onClose()}>
      <DialogContent className={cn("max-h-[calc(100dvh-2rem)] overflow-y-auto", wide ? "sm:max-w-2xl" : "sm:max-w-md")}>
        <DialogHeader>
          <DialogTitle className="text-lg">{title}</DialogTitle>
          {description ? <DialogDescription>{description}</DialogDescription> : <DialogDescription className="sr-only">{title}</DialogDescription>}
        </DialogHeader>
        {children}
      </DialogContent>
    </Dialog>
  );
}

/** The action row at the bottom of a dialog form. */
export function DialogActions({ children }: { children: React.ReactNode }) {
  return <div className="mt-2 flex flex-wrap justify-end gap-2">{children}</div>;
}

/** A labelled form control; the label wraps the control, so clicking it focuses the control. */
export function Field({
  label,
  hint,
  children,
  className,
}: {
  label: string;
  hint?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
}) {
  return (
    <label className={cn("grid gap-1.5", className)}>
      <span className="text-xs font-medium text-muted-foreground">{label}</span>
      {children}
      {hint && <span className="text-xs text-muted-foreground">{hint}</span>}
    </label>
  );
}

export interface Message {
  text: string;
  kind: "" | "ok" | "error";
}

export function Msg({ msg, className }: { msg: Message; className?: string }) {
  if (!msg.text) return null;
  const Icon = msg.kind === "error" ? AlertCircle : CheckCircle2;
  return (
    <p
      role={msg.kind === "error" ? "alert" : "status"}
      className={cn(
        "flex items-start gap-2 rounded-lg px-3 py-2 text-sm",
        msg.kind === "error" ? "bg-destructive/10 text-destructive" : "bg-success/10 text-success",
        className,
      )}
    >
      {msg.kind && <Icon className="mt-0.5 size-4 shrink-0" />}
      <span>{msg.text}</span>
    </p>
  );
}

/** An error shown in place of content. */
export function ErrorText({ children }: { children: React.ReactNode }) {
  return <Msg msg={{ text: String(children), kind: "error" }} />;
}

/**
 * Runs async actions with a pending flag and a message: errors are shown,
 * and a returned string is shown as success.
 */
export function useAction() {
  const [pending, setPending] = useState(false);
  const [msg, setMsg] = useState<Message>({ text: "", kind: "" });
  const run = async (fn: () => Promise<string | void>) => {
    setPending(true);
    setMsg({ text: "", kind: "" });
    try {
      const ok = await fn();
      if (ok) setMsg({ text: ok, kind: "ok" });
      return true;
    } catch (e) {
      setMsg({ text: errorMessage(e), kind: "error" });
      return false;
    } finally {
      setPending(false);
    }
  };
  return { pending, msg, setMsg, run };
}

export function Pager<T>({ res, onPage }: { res: Page<T> | null; onPage: (page: number) => void }) {
  if (!res || res.total_pages <= 1) return null;
  return (
    <nav className="flex items-center justify-between gap-2 pt-2" aria-label="Pages">
      <Button variant="outline" size="sm" disabled={res.page <= 1} onClick={() => onPage(res.page - 1)}>
        <ChevronLeft data-icon="inline-start" /> Prev
      </Button>
      <span className="text-xs text-muted-foreground tabular-nums">
        Page {res.page} of {res.total_pages} · {res.total} total
      </span>
      <Button variant="outline" size="sm" disabled={res.page >= res.total_pages} onClick={() => onPage(res.page + 1)}>
        Next <ChevronRight data-icon="inline-end" />
      </Button>
    </nav>
  );
}

export function VisibilityBadge({ visibility }: { visibility: string }) {
  const isPublic = visibility === "public";
  return (
    <Badge variant="outline" className={cn(isPublic && "border-success/30 bg-success/10 text-success")}>
      {isPublic ? <Globe /> : <Lock />}
      {visibility}
    </Badge>
  );
}

export function RoleBadge({ role }: { role: string }) {
  return (
    <Badge variant="outline" className="border-warning/30 bg-warning/10 text-warning">
      {role}
    </Badge>
  );
}

export function ProgressBar({ value, status }: { value: number; status?: string }) {
  return (
    <Progress
      value={Math.max(0, Math.min(100, value))}
      className={cn(
        "h-1.5",
        status === "failed" && "[&>[data-slot=progress-indicator]]:bg-destructive",
        status === "queued" && "[&>[data-slot=progress-indicator]]:bg-muted-foreground/50",
        status === "canceled" && "[&>[data-slot=progress-indicator]]:bg-muted-foreground/50",
      )}
    />
  );
}

/** Asks for confirmation in a dialog before running onConfirm. */
export function ConfirmButton({
  label,
  question,
  confirmLabel,
  onConfirm,
  variant = "outline",
  size,
  destructive,
  icon,
}: {
  label: string;
  question: string;
  confirmLabel?: string;
  onConfirm: () => unknown;
  variant?: React.ComponentProps<typeof Button>["variant"];
  size?: React.ComponentProps<typeof Button>["size"];
  destructive?: boolean;
  icon?: React.ReactNode;
}) {
  const [asking, setAsking] = useState(false);
  const { pending, msg, run } = useAction();
  return (
    <>
      <Button type="button" variant={destructive && variant === "outline" ? "destructive" : variant} size={size} onClick={() => setAsking(true)}>
        {icon}
        {label}
      </Button>
      <AlertDialog open={asking} onOpenChange={setAsking}>
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>{label}</AlertDialogTitle>
            <AlertDialogDescription>{question}</AlertDialogDescription>
          </AlertDialogHeader>
          <Msg msg={msg} />
          <AlertDialogFooter>
            <AlertDialogCancel>Cancel</AlertDialogCancel>
            <Button
              type="button"
              className={cn(destructive && "bg-destructive text-white hover:bg-destructive/90")}
              disabled={pending}
              onClick={() =>
                run(async () => {
                  await onConfirm();
                  setAsking(false);
                })
              }
            >
              {confirmLabel || label}
            </Button>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </>
  );
}

/** A page's title row: heading, description and actions. */
export function PageHeader({
  title,
  description,
  actions,
}: {
  title: string;
  description?: React.ReactNode;
  actions?: React.ReactNode;
}) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-4">
      <div className="min-w-0">
        <h1 className="text-3xl font-semibold sm:text-4xl">{title}</h1>
        {description && <p className="mt-1.5 max-w-prose text-sm text-muted-foreground">{description}</p>}
      </div>
      {actions && <div className="flex flex-wrap gap-2">{actions}</div>}
    </div>
  );
}

/** A friendly placeholder for empty lists and missing content, on contour lines. */
export function EmptyState({
  icon,
  title,
  children,
  className,
}: {
  icon?: React.ReactNode;
  title: string;
  children?: React.ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "topo flex flex-col items-center gap-2 overflow-hidden rounded-2xl border border-dashed px-6 py-10 text-center",
        className,
      )}
    >
      {icon && <div className="mb-1 grid size-11 place-items-center rounded-full bg-accent text-accent-foreground">{icon}</div>}
      <p className="font-heading text-base font-semibold">{title}</p>
      {children && <div className="max-w-sm text-sm text-muted-foreground">{children}</div>}
    </div>
  );
}

/** One figure with its label. */
export function Stat({ label, value, className }: { label: string; value: React.ReactNode; className?: string }) {
  return (
    <div className={cn("stat min-w-0", className)}>
      <div className="figure truncate text-xl">{value}</div>
      <div className="text-xs text-muted-foreground">{label}</div>
    </div>
  );
}

/** Figures in a responsive grid. */
export function StatGrid({ stats, className }: { stats: [string, React.ReactNode][]; className?: string }) {
  return (
    <div className={cn("grid grid-cols-2 gap-x-4 gap-y-3 sm:grid-cols-3", className)}>
      {stats.map(([label, value]) => (
        <Stat key={label} label={label} value={value} />
      ))}
    </div>
  );
}

/** The centered content column of the list pages. */
export function PageShell({ children, className }: { children: React.ReactNode; className?: string }) {
  return <div className={cn("mx-auto grid w-full max-w-5xl gap-6 px-4 py-8 sm:px-6 sm:py-10", className)}>{children}</div>;
}

/** Placeholder while a page loads. */
export function PageLoading({ label = "Loading…" }: { label?: string }) {
  return (
    <PageShell>
      <div className="grid gap-3" aria-busy="true" aria-label={label}>
        <div className="h-9 w-48 animate-pulse rounded-lg bg-muted" />
        <div className="h-4 w-72 animate-pulse rounded bg-muted" />
        <div className="mt-4 h-40 animate-pulse rounded-2xl bg-muted" />
      </div>
    </PageShell>
  );
}

const jobStatusStyles: Record<string, string> = {
  queued: "border-border bg-muted text-muted-foreground",
  running: "border-primary/30 bg-primary/10 text-primary",
  succeeded: "border-success/30 bg-success/10 text-success",
  failed: "border-destructive/30 bg-destructive/10 text-destructive",
  canceled: "border-border bg-muted text-muted-foreground",
};

/** An import job's status as a colored badge. */
export function StatusBadge({ status, children }: { status: string; children: React.ReactNode }) {
  return (
    <Badge variant="outline" className={cn("h-auto max-w-full py-0.5 whitespace-normal", jobStatusStyles[status])}>
      {children}
    </Badge>
  );
}
