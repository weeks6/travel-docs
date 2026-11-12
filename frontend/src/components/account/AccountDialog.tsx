"use client";

import { useState } from "react";
import { authApi, setSession, setUser } from "@/lib/api";
import type { User } from "@/lib/types";
import { DialogActions, Field, Modal, Msg, useAction } from "../common";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "../ui/tabs";

export function AccountDialog({ user, onClose }: { user: User; onClose: () => void }) {
  const profile = useAction();
  const password = useAction();
  const sessions = useAction();
  const del = useAction();
  const [confirmDelete, setConfirmDelete] = useState(false);
  return (
    <Modal title="Account" description={user.email} onClose={onClose}>
      <Tabs defaultValue="profile" className="gap-4">
        <TabsList className="w-full">
          <TabsTrigger value="profile">Profile</TabsTrigger>
          <TabsTrigger value="password">Password</TabsTrigger>
          <TabsTrigger value="sessions">Sessions</TabsTrigger>
          <TabsTrigger value="delete">Danger</TabsTrigger>
        </TabsList>

        <TabsContent value="profile">
          <form
            className="grid gap-4"
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
              <Input name="display_name" defaultValue={user.display_name} required maxLength={100} />
            </Field>
            <Field label="Email">
              <Input type="email" name="email" defaultValue={user.email} required />
            </Field>
            <Field label="Current password" hint="Needed only to change your email.">
              <Input type="password" name="current_password" autoComplete="current-password" />
            </Field>
            <Msg msg={profile.msg} />
            <DialogActions>
              <Button type="submit" disabled={profile.pending}>
                Save profile
              </Button>
            </DialogActions>
          </form>
        </TabsContent>

        <TabsContent value="password">
          <form
            className="grid gap-4"
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
              <Input type="password" name="current_password" required autoComplete="current-password" />
            </Field>
            <Field label="New password" hint="At least 8 characters.">
              <Input type="password" name="new_password" required minLength={8} autoComplete="new-password" />
            </Field>
            <Msg msg={password.msg} />
            <DialogActions>
              <Button type="submit" disabled={password.pending}>
                Change password
              </Button>
            </DialogActions>
          </form>
        </TabsContent>

        <TabsContent value="sessions" className="grid gap-4">
          <p className="text-sm text-muted-foreground">
            Ends your session on every device, including this one. You will need to log in again.
          </p>
          <Msg msg={sessions.msg} />
          <DialogActions>
            <Button
              variant="outline"
              disabled={sessions.pending}
              onClick={() =>
                sessions.run(async () => {
                  await authApi.logoutAll();
                  setSession(null);
                  onClose();
                })
              }
            >
              Log out everywhere
            </Button>
          </DialogActions>
        </TabsContent>

        <TabsContent value="delete">
          <form
            className="grid gap-4"
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
            <p className="text-sm text-muted-foreground">
              Deletes your account with all your routes, collections and trips. This cannot be undone.
            </p>
            <Field label="Password">
              <Input type="password" name="password" required autoComplete="current-password" />
            </Field>
            {confirmDelete && (
              <Msg msg={{ text: "Are you sure? Press the button again to delete everything.", kind: "error" }} />
            )}
            <Msg msg={del.msg} />
            <DialogActions>
              <Button
                type="submit"
                variant={confirmDelete ? "default" : "destructive"}
                className={confirmDelete ? "bg-destructive text-white hover:bg-destructive/90" : undefined}
                disabled={del.pending}
              >
                {confirmDelete ? "Yes, delete everything" : "Delete my account"}
              </Button>
            </DialogActions>
          </form>
        </TabsContent>
      </Tabs>
    </Modal>
  );
}
