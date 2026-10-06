import { describe, expect, it } from "vitest";
import type { ChannelOverview } from "../../api/types";
import {
  relayCredentialFor,
  userCredentialFor,
  type PickableCredential,
} from "./channelCredentials";

/**
 * These two pickers decide which stored token a button acts on. Getting them
 * wrong is silent: "edit this token" would touch a credential the gateway never
 * schedules, or a relay action would fire against a token of the wrong kind.
 */
const credential = (over: Partial<PickableCredential> = {}): PickableCredential => ({
  id: 1,
  site_id: 7,
  kind: "access_token",
  status: "enabled",
  has_secret: true,
  checkin_enabled: false,
  ...over,
});

const overview = (over: Partial<ChannelOverview["channel"]> = {}): ChannelOverview =>
  ({ channel: { site_id: 7, credential_id: 0, ...over } }) as ChannelOverview;

describe("userCredentialFor", () => {
  it("ignores credentials from another site and disabled ones", () => {
    const list = [
      credential({ id: 1, site_id: 9 }),
      credential({ id: 2, status: "disabled" }),
      credential({ id: 3 }),
    ];
    expect(userCredentialFor(overview(), list)?.id).toBe(3);
  });

  it("prefers the credential the channel is bound to", () => {
    const list = [credential({ id: 1 }), credential({ id: 2, checkin_enabled: true })];
    expect(userCredentialFor(overview({ credential_id: 2 }), list)?.id).toBe(2);
  });

  it("prefers a scheduled credential when the schedule is on", () => {
    const list = [credential({ id: 1 }), credential({ id: 2, checkin_enabled: true })];
    const withSchedule = { ...overview(), checkin_enabled: true } as ChannelOverview;
    expect(userCredentialFor(withSchedule, list)?.id).toBe(2);
  });

  it("prefers an unscheduled credential when the schedule is off", () => {
    const list = [credential({ id: 1, checkin_enabled: true }), credential({ id: 2 })];
    expect(userCredentialFor(overview(), list)?.id).toBe(2);
  });

  it("accepts a session credential, and falls back to the first one on site", () => {
    const list = [credential({ id: 1, kind: "session", checkin_enabled: true })];
    expect(userCredentialFor(overview(), list)?.id).toBe(1);
    expect(userCredentialFor(overview(), [])).toBeUndefined();
    expect(userCredentialFor(undefined, list)?.id).toBe(1);
  });
});

describe("relayCredentialFor", () => {
  it("takes the bound credential when it is an api key", () => {
    const list = [credential({ id: 1, kind: "api_key" }), credential({ id: 2, kind: "api_key" })];
    expect(relayCredentialFor(overview({ credential_id: 2 }), list)?.id).toBe(2);
  });

  it("never returns a credential of the wrong kind", () => {
    const list = [credential({ id: 1, kind: "access_token" })];
    expect(relayCredentialFor(overview({ credential_id: 1 }), list)).toBeUndefined();
  });

  it("falls back to any enabled api key", () => {
    const list = [
      credential({ id: 1, kind: "api_key", status: "disabled" }),
      credential({ id: 2, kind: "api_key" }),
    ];
    expect(relayCredentialFor(overview(), list)?.id).toBe(2);
  });
});
