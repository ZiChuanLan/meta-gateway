import { withCredentialMetaValue } from "../credentialMeta";

/**
 * The stored credential as this patch needs to see it: only the fields that
 * decide what to send. Callers hold a richer record (site, status, schedule) and
 * structurally satisfy this.
 */
export type PatchableCredential = {
  id?: number;
  kind?: string;
  has_secret?: boolean;
  has_cookie?: boolean;
  meta_json?: string;
};

/**
 * The credential fields that actually changed, ready to send.
 *
 * Sending the whole credential back would blank everything the dialog did not
 * carry (the mask means "keep the stored value"), so the create/edit path builds
 * a patch of only the deltas — and the rules for what counts as a delta are worth
 * reading on their own rather than as a closure over eight locals:
 *
 *  - a masked field is untouched, and an *empty* field means "remove" only when
 *    something was actually stored — otherwise every save of an untouched dialog
 *    would fire a pointless credential write;
 *  - `kind` is preserved when the stored credential is a session, because
 *    downgrading it to an access token on an unrelated edit would change how
 *    check-ins authenticate;
 *  - the numeric platform user id rides in meta_json, and only when it changed.
 */
export type CredentialPatchInput = {
  userCred?: PatchableCredential;
  userToken: string;
  userCookie: string;
  userID: number | undefined;
  userAuthMode: string;
  secretChanged: boolean;
  cookieChanged: boolean;
  metaChanged: boolean;
};

export function credentialPatch(input: CredentialPatchInput): Record<string, unknown> {
  const { userCred, userToken, userCookie, userID, userAuthMode } = input;
  const patch: Record<string, unknown> = {};
  if (input.secretChanged) {
    patch.kind =
      userCred?.kind === "session" || userCred?.kind === "access_token"
        ? userCred.kind
        : "access_token";
    if (userToken) patch.secret = userToken;
    else patch.clear_secret = true;
  }
  if (input.cookieChanged) {
    if (userCookie) patch.cookie = userCookie;
    else patch.clear_cookie = true;
  }
  if (input.metaChanged) {
    patch.meta_json = withCredentialMetaValue(userCred?.meta_json, "platform_user_id", userID);
  }
  if (input.secretChanged || input.cookieChanged) {
    patch.auth_mode = userAuthMode;
    patch.status = "enabled";
  }
  return patch;
}

/**
 * Both auth materials were explicitly cleared: the credential itself goes away
 * rather than lingering as an empty row.
 */
export function credentialShouldBeRemoved(input: {
  userCred?: PatchableCredential;
  userToken: string;
  userCookie: string;
  secretChanged: boolean;
  cookieChanged: boolean;
}): boolean {
  return (
    Boolean(input.userCred?.id) &&
    input.cookieChanged &&
    input.secretChanged &&
    !input.userToken &&
    !input.userCookie
  );
}
