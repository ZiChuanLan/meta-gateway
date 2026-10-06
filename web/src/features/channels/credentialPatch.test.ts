import { describe, expect, it } from "vitest";
import {
  credentialPatch,
  credentialShouldBeRemoved,
  type PatchableCredential,
} from "./credentialPatch";

const stored: PatchableCredential = {
  id: 5,
  kind: "access_token",
  has_secret: true,
  has_cookie: true,
  meta_json: JSON.stringify({ platform_user_id: 1544 }),
};

/** The common case: nothing touched, so nothing is sent. */
const unchanged = {
  userCred: stored,
  userToken: "",
  userCookie: "",
  userID: 1544,
  userAuthMode: "access_token",
  secretChanged: false,
  cookieChanged: false,
  metaChanged: false,
};

describe("credentialPatch", () => {
  it("sends nothing when the operator changed nothing", () => {
    expect(credentialPatch(unchanged)).toEqual({});
  });

  it("carries a new secret with the stored kind and a reason to be enabled", () => {
    expect(
      credentialPatch({ ...unchanged, userToken: "fresh-token", secretChanged: true }),
    ).toEqual({
      kind: "access_token",
      secret: "fresh-token",
      auth_mode: "access_token",
      status: "enabled",
    });
  });

  it("asks for removal when a stored secret is cleared", () => {
    expect(credentialPatch({ ...unchanged, secretChanged: true })).toEqual({
      kind: "access_token",
      clear_secret: true,
      auth_mode: "access_token",
      status: "enabled",
    });
  });

  it("keeps a session credential a session", () => {
    const patch = credentialPatch({
      ...unchanged,
      userCred: { ...stored, kind: "session" },
      userToken: "another",
      secretChanged: true,
    });
    expect(patch.kind).toBe("session");
  });

  it("writes the numeric user id only when it changed", () => {
    expect(credentialPatch({ ...unchanged, userID: 9001, metaChanged: true }).meta_json).toBe(
      JSON.stringify({ platform_user_id: 9001 }),
    );
    expect(credentialPatch(unchanged).meta_json).toBeUndefined();
  });

  it("treats clearing both materials as a removal, not an empty patch", () => {
    const input = { ...unchanged, secretChanged: true, cookieChanged: true };
    expect(credentialPatch(input)).toMatchObject({ clear_secret: true, clear_cookie: true });
    expect(credentialShouldBeRemoved(input)).toBe(true);
    // A replacement value on either side keeps the credential alive.
    expect(credentialShouldBeRemoved({ ...input, userCookie: "cookie=1" })).toBe(false);
    expect(credentialShouldBeRemoved({ ...input, userToken: "token" })).toBe(false);
  });
});
