"use client";

import { LogIn } from "lucide-react";
import { useState } from "react";
import { authApi, setSession } from "@/lib/api";
import { DialogActions, EmptyState, Field, Modal, Msg, useAction } from "../common";
import { Button } from "../ui/button";
import { Input } from "../ui/input";

/** Invites anonymous visitors to log in or sign up (e.g. "Log in to upload"). */
export function LoginPrompt({ text, title = "Log in to continue" }: { text: string; title?: string }) {
  const [mode, setMode] = useState<null | "login" | "register">(null);
  return (
    <EmptyState icon={<LogIn className="size-5" />} title={title}>
      <p>{text}</p>
      <div className="mt-4 flex justify-center gap-2">
        <Button variant="outline" onClick={() => setMode("login")}>
          Log in
        </Button>
        <Button onClick={() => setMode("register")}>Create an account</Button>
      </div>
      {mode && <LoginDialog key={mode} mode={mode} onMode={setMode} onClose={() => setMode(null)} />}
    </EmptyState>
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
    <Modal
      title={register ? "Create account" : "Welcome back"}
      description={register ? "Keep your routes and trips in one place." : "Log in to your routes and trips."}
      onClose={onClose}
    >
      <form
        className="grid gap-4"
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
          <Input type="email" name="email" required autoComplete="email" />
        </Field>
        {register && (
          <Field label="Display name">
            <Input name="display_name" maxLength={100} placeholder="Optional" />
          </Field>
        )}
        <Field label="Password" hint={register ? "At least 8 characters." : undefined}>
          <Input
            type="password"
            name="password"
            required
            minLength={register ? 8 : undefined}
            autoComplete={register ? "new-password" : "current-password"}
          />
        </Field>
        <Msg msg={msg} />
        <Button type="submit" size="lg" className="w-full" disabled={pending}>
          {register ? "Create account" : "Log in"}
        </Button>
      </form>
      <DialogActions>
        <Button variant="link" className="mx-auto h-auto p-0" onClick={() => onMode(register ? "login" : "register")}>
          {register ? "Already have an account? Log in" : "No account yet? Sign up"}
        </Button>
      </DialogActions>
    </Modal>
  );
}
