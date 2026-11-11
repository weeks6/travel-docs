"use client";

import { useEffect, useRef, useState } from "react";
import { ApiError } from "@/lib/api";
import type { Page } from "@/lib/types";

export function errorMessage(e: unknown): string {
  if (e instanceof ApiError) return e.message;
  if (e instanceof Error) return e.message;
  return String(e);
}

/** A modal dialog, open while rendered. */
export function Modal({
  title,
  onClose,
  children,
  wide,
}: {
  title: string;
  onClose: () => void;
  children: React.ReactNode;
  wide?: boolean;
}) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const d = ref.current;
    if (d && !d.open) d.showModal();
    return () => d?.close();
  }, []);
  return (
    <dialog
      ref={ref}
      className={wide ? "wide" : undefined}
      onCancel={(e) => {
        e.preventDefault();
        onClose();
      }}
    >
      <div className="dialog-head">
        <h3>{title}</h3>
        <button type="button" className="icon" aria-label="Close" onClick={onClose}>
          ×
        </button>
      </div>
      <div className="dialog-body">{children}</div>
    </dialog>
  );
}

export function Field({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <label className="field">
      <span>{label}</span>
      {children}
    </label>
  );
}

export interface Message {
  text: string;
  kind: "" | "ok" | "error";
}

export function Msg({ msg }: { msg: Message }) {
  return <div className={"msg" + (msg.kind ? " " + msg.kind : "")}>{msg.text}</div>;
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
    <div className="pager">
      <button type="button" disabled={res.page <= 1} onClick={() => onPage(res.page - 1)}>
        ‹ Prev
      </button>
      <span>
        Page {res.page} of {res.total_pages} · {res.total} total
      </span>
      <button type="button" disabled={res.page >= res.total_pages} onClick={() => onPage(res.page + 1)}>
        Next ›
      </button>
    </div>
  );
}

export function VisibilityBadge({ visibility }: { visibility: string }) {
  return <span className={"badge" + (visibility === "public" ? " public" : "")}>{visibility}</span>;
}

export function RoleBadge({ role }: { role: string }) {
  return <span className="badge role">{role}</span>;
}

export function ProgressBar({ value, status }: { value: number; status?: string }) {
  return (
    <div className={"progress " + (status || "")} role="progressbar" aria-valuenow={value} aria-valuemin={0} aria-valuemax={100}>
      <div style={{ width: Math.max(0, Math.min(100, value)) + "%" }} />
    </div>
  );
}

/** Asks for confirmation inside the page (instead of window.confirm). */
export function ConfirmButton({
  label,
  question,
  confirmLabel,
  onConfirm,
  className,
}: {
  label: string;
  question: string;
  confirmLabel?: string;
  onConfirm: () => unknown;
  className?: string;
}) {
  const [asking, setAsking] = useState(false);
  const { pending, msg, run } = useAction();
  return (
    <>
      <button type="button" className={className} onClick={() => setAsking(true)}>
        {label}
      </button>
      {asking && (
        <Modal title={label} onClose={() => setAsking(false)}>
          <p>{question}</p>
          <Msg msg={msg} />
          <div className="dialog-actions">
            <button type="button" onClick={() => setAsking(false)}>
              Cancel
            </button>
            <button
              type="button"
              className={className === "danger" ? "danger-solid" : "primary"}
              disabled={pending}
              onClick={() =>
                run(async () => {
                  await onConfirm();
                  setAsking(false);
                })
              }
            >
              {confirmLabel || label}
            </button>
          </div>
        </Modal>
      )}
    </>
  );
}
