"use client";

import { useState } from "react";
import { authApi, setSession } from "@/lib/api";
import { Field, Modal, Msg, useAction } from "../ui";

/** Opens the login dialog from anywhere (e.g. "Log in to upload"). */
export function LoginPrompt({ text }: { text: string }) {
  const [mode, setMode] = useState<null | "login" | "register">(null);
  return (
    <div className="empty">
      {text}{" "}
      <button type="button" className="link" onClick={() => setMode("login")}>
        Log in
      </button>{" "}
      or{" "}
      <button type="button" className="link" onClick={() => setMode("register")}>
        sign up
      </button>
      .
      {mode && <LoginDialog key={mode} mode={mode} onMode={setMode} onClose={() => setMode(null)} />}
    </div>
  );
}

export function LoginDialog({
  mode,
  onMode,
  onClose,
}: {
  mode: "login" | "register";
  onMode: (m: "login" | "register") => void;
  onClose: () => void;
}) {
  const register = mode === "register";
  const { pending, msg, run } = useAction();
  return (
    <Modal title={register ? "Create account" : "Log in"} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const fd = Object.fromEntries(new FormData(e.currentTarget));
          run(async () => {
            const s = await (register ? authApi.register(fd) : authApi.login(fd));
            setSession(s);
            onClose();
          });
        }}
      >
        <Field label="Email">
          <input type="email" name="email" required autoComplete="email" />
        </Field>
        {register && (
          <Field label="Display name">
            <input name="display_name" maxLength={100} placeholder="Optional" />
          </Field>
        )}
        <Field label="Password">
          <input
            type="password"
            name="password"
            required
            minLength={register ? 8 : undefined}
            autoComplete={register ? "new-password" : "current-password"}
          />
        </Field>
        <Msg msg={msg} />
        <div className="dialog-actions">
          <button type="submit" className="primary" disabled={pending}>
            {register ? "Create account" : "Log in"}
          </button>
        </div>
      </form>
      <button type="button" className="link" onClick={() => onMode(register ? "login" : "register")}>
        {register ? "Already have an account? Log in" : "No account yet? Sign up"}
      </button>
    </Modal>
  );
}
