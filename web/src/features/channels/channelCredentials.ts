import type { ChannelOverview } from "../../api/types";

/**
 * Which stored credential a channel's buttons should act on.
 *
 * Both functions were closures inside the channels page; the rules they encode
 * are the interesting part and deserve to be readable (and tested) on their own:
 *
 *  - the check-in / account token must be **the same credential the gateway will
 *    schedule**, otherwise "edit this token" and "the check-in that runs at 04:00"
 *    silently diverge — so the channel's bound credential wins, then a credential
 *    that is actually scheduled, then one that is not yet scheduled;
 *  - the relay key must be an **enabled api_key** — the bound one when it matches,
 *    otherwise any enabled key, because a credential of the wrong kind would make
 *    every relay action fail.
 */
export type PickableCredential = {
  id: number;
  site_id: number;
  kind: string;
  /** The stored status as the API reports it ("enabled" / "disabled" / "auto_disabled"). */
  status: string;
  auth_mode?: string;
  has_secret: boolean;
  has_cookie?: boolean;
  checkin_enabled: boolean;
  meta_json?: string;
};

/** The credential that signs in as the site's user (access token or session). */
export function userCredentialFor(
  overview: ChannelOverview | undefined,
  credentials: PickableCredential[],
): PickableCredential | undefined {
  const siteId = overview?.channel.site_id;
  const onSite = credentials.filter((item) => {
    if (siteId != null && item.site_id !== siteId) return false;
    return (item.kind === "access_token" || item.kind === "session") && item.status === "enabled";
  });
  if (!onSite.length) return undefined;
  // Match backend pickUserCredential: prefer the credential this channel is bound
  // to, so editing/deleting the token operates on the same credential checks use.
  const boundId = overview?.channel.credential_id;
  if (boundId) {
    const bound = onSite.find((item) => item.id === boundId);
    if (bound) return bound;
  }
  // Match the badge: when the schedule is on, operate on a credential that is
  // actually scheduled.
  if (overview?.checkin_enabled) {
    const scheduled = onSite.find((item) => item.checkin_enabled);
    if (scheduled) return scheduled;
  }
  // When the schedule is off, prefer a token that is not scheduled yet (first
  // off, else any).
  const notScheduled = onSite.find((item) => !item.checkin_enabled);
  return notScheduled ?? onSite[0];
}

/** The api_key that carries relay traffic for this channel. */
export function relayCredentialFor(
  overview: ChannelOverview,
  credentials: PickableCredential[],
): PickableCredential | undefined {
  const id = overview.channel.credential_id;
  if (id) {
    const hit = credentials.find((item) => item.id === id);
    if (hit && hit.kind === "api_key") return hit;
  }
  return credentials.find((item) => item.kind === "api_key" && item.status === "enabled");
}
