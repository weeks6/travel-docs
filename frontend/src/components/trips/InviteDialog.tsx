"use client";

import { useEffect, useState } from "react";
import { tripsApi } from "@/lib/api";
import { dateTime } from "@/lib/format";
import type { Invitation, Trip } from "@/lib/types";
import { Modal, Msg, useAction } from "../ui";

export function InviteDialog({ trip, onClose }: { trip: Trip; onClose: () => void }) {
  const [invitations, setInvitations] = useState<Invitation[] | null>(null);
  const [version, setVersion] = useState(0);
  const invite = useAction();
  const revoke = useAction();
  useEffect(() => {
    let active = true;
    tripsApi
      .invitations(trip.id)
      .then((r) => active && setInvitations(r.items))
      .catch(() => undefined);
    return () => {
      active = false;
    };
  }, [trip.id, version]);

  return (
    <Modal title={`Invite to “${trip.name}”`} onClose={onClose}>
      <form
        onSubmit={(e) => {
          e.preventDefault();
          const form = e.currentTarget;
          const email = String(new FormData(form).get("email") || "");
          invite.run(async () => {
            await tripsApi.invite(trip.id, email);
            form.reset();
            setVersion((v) => v + 1);
            return "Invitation sent";
          });
        }}
      >
        <div className="row">
          <input type="email" name="email" placeholder="Their account email" required />
          <button type="submit" className="primary" disabled={invite.pending}>
            Invite
          </button>
        </div>
        <Msg msg={invite.msg} />
      </form>
      <p className="meta">They see the invitation under Trips and can join or decline.</p>
      <h4>Invitations</h4>
      <table className="table">
        <tbody>
          {invitations?.map((inv) => (
            <tr key={inv.id}>
              <td>
                {inv.invitee_name}
                <div className="meta">
                  {inv.invitee_email} · {dateTime(inv.created_at)}
                </div>
              </td>
              <td>
                {inv.status}{" "}
                {inv.status === "pending" && (
                  <button
                    type="button"
                    className="link"
                    disabled={revoke.pending}
                    onClick={() =>
                      revoke.run(async () => {
                        await tripsApi.revokeInvitation(trip.id, inv.id);
                        setVersion((v) => v + 1);
                      })
                    }
                  >
                    Withdraw
                  </button>
                )}
              </td>
            </tr>
          ))}
          {invitations?.length === 0 && (
            <tr>
              <td className="meta">No invitations yet.</td>
            </tr>
          )}
        </tbody>
      </table>
      <Msg msg={revoke.msg} />
    </Modal>
  );
}
