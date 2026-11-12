"use client";

import { useState } from "react";
import { authApi, setSession, setUser } from "@/lib/api";
import type { User } from "@/lib/types";
import { Field, Modal, Msg, useAction } from "../ui";

export function AccountDialog({ user, onClose }: { user: User; onClose: () => void }) {
  const profile = useAction();
  const password = useAction();
  const sessions = useAction();
  const del = useAction();
  const [confirmDelete, setConfirmDelete] = useState(false);
  return (
    <Modal title="Account" onClose={onClose}>
      <h4>Profile</h4>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const fd = Object.fromEntries(new FormData(e.currentTarget));
          profile.run(async () => {
            setUser(await authApi.updateMe(fd));
            return "Saved";
          });
        }}
      >
        <Field label="Display name">
          <input name="display_name" defaultValue={user.display_name} required maxLength={100} />
        </Field>
        <Field label="Email">
          <input type="email" name="email" defaultValue={user.email} required />
        </Field>
        <Field label="Current password (needed to change email)">
          <input type="password" name="current_password" autoComplete="current-password" />
        </Field>
        <Msg msg={profile.msg} />
        <div className="dialog-actions">
          <button type="submit" className="primary" disabled={profile.pending}>
            Save profile
          </button>
        </div>
      </form>

      <h4>Password</h4>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const form = e.currentTarget;
          const fd = Object.fromEntries(new FormData(form));
          password.run(async () => {
            setSession(await authApi.changePassword(fd));
            form.reset();
            return "Password changed. Other devices were logged out.";
          });
        }}
      >
        <Field label="Current password">
          <input type="password" name="current_password" required autoComplete="current-password" />
        </Field>
        <Field label="New password">
          <input type="password" name="new_password" required minLength={8} autoComplete="new-password" />
        </Field>
        <Msg msg={password.msg} />
        <div className="dialog-actions">
          <button type="submit" className="primary" disabled={password.pending}>
            Change password
          </button>
        </div>
      </form>

      <h4>Sessions</h4>
      <Msg msg={sessions.msg} />
      <div className="dialog-actions">
        <button
          type="button"
          disabled={sessions.pending}
          onClick={() =>
            sessions.run(async () => {
              await authApi.logoutAll();
              setSession(null);
              onClose();
            })
          }
        >
          Log out everywhere (including here)
        </button>
      </div>

      <h4>Delete account</h4>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          if (!confirmDelete) {
            setConfirmDelete(true);
            return;
          }
          const fd = Object.fromEntries(new FormData(e.currentTarget));
          del.run(async () => {
            await authApi.deleteMe(fd);
            setSession(null);
            onClose();
          });
        }}
      >
        <Field label="Password">
          <input type="password" name="password" required autoComplete="current-password" />
        </Field>
        {confirmDelete && (
          <p className="msg error">
            This deletes your account with all your routes, collections and trips. It cannot be undone. Press the
            button again to confirm.
          </p>
        )}
        <Msg msg={del.msg} />
        <div className="dialog-actions">
          <button type="submit" className="danger" disabled={del.pending}>
            {confirmDelete ? "Yes, delete everything" : "Delete my account"}
          </button>
        </div>
      </form>
    </Modal>
  );
}
