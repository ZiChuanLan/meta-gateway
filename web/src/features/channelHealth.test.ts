import { describe, expect, it } from "vitest";
import type { ChannelOverview } from "../api/types";
import {
  channelAccountState,
  channelConnectivityState,
  channelHealthState,
  channelNeedsAttention,
  channelReadiness,
  isChannelReady,
} from "./channelHealth";

function overview(overrides: Partial<ChannelOverview> = {}): ChannelOverview {
  return {
    channel: {
      id: 1,
      name: "demo",
      base_url: "https://api.example.com",
      models_csv: "gpt-test",
      group_name: "default",
      priority: 0,
      weight: 100,
      status: "enabled",
      created_at: "",
      updated_at: "",
    },
    checkin_enabled: false,
    checkin_supported: true,
    account_supported: true,
    has_user_credential: false,
    has_platform_user_id: false,
    has_api_key: true,
    site_usable: true,
    credential_usable: true,
    model_count: 1,
    last_latency_ms: 100,
    route_count: 1,
    enabled_member_count: 1,
    cooling_member_count: 0,
    failure_count: 0,
    ...overrides,
  };
}

describe("channel health dimensions", () => {
  it("trusts the backend business-health verdict over local re-derivation", () => {
    const value = overview({
      health_state: "degraded",
      last_probe_ok: true,
      failure_count: 0,
    });
    expect(channelHealthState(value)).toBe("degraded");
    expect(channelReadiness(value)).toBe("degraded");
    expect(isChannelReady(value)).toBe(false);
  });

  it("keeps a missing API key as readiness, not network failure", () => {
    const value = overview({
      health_state: "unknown",
      has_api_key: false,
      last_ping_at: "2026-08-09T00:00:00Z",
      last_ping_ok: true,
    });
    expect(channelReadiness(value)).toBe("missing_key");
    expect(channelHealthState(value)).toBe("unknown");
    expect(channelConnectivityState(value)).toBe("reachable");
    expect(channelNeedsAttention(value)).toBe(false);
  });

  it("distinguishes unknown, reachable, and unreachable Ping states", () => {
    expect(channelConnectivityState(overview())).toBe("unknown");
    expect(
      channelConnectivityState(
        overview({
          connectivity_state: "reachable",
          last_ping_at: "2026-08-09T00:00:00Z",
          last_ping_ok: false,
        }),
      ),
    ).toBe("reachable");
    expect(
      channelConnectivityState(
        overview({ last_ping_at: "2026-08-09T00:00:00Z", last_ping_ok: true }),
      ),
    ).toBe("reachable");
    expect(
      channelConnectivityState(
        overview({ last_ping_at: "2026-08-09T00:00:00Z", last_ping_ok: false }),
      ),
    ).toBe("unreachable");
    expect(channelConnectivityState(overview(), { reachable: true })).toBe(
      "reachable",
    );
    expect(channelConnectivityState(overview(), { reachable: false })).toBe(
      "unreachable",
    );
  });

  it("marks route degradation for attention without calling it unreachable", () => {
    const value = overview({
      health_state: "degraded",
      health_reason: "route_cooling",
      cooling_member_count: 1,
      last_ping_at: "2026-08-09T00:00:00Z",
      last_ping_ok: true,
    });
    expect(channelReadiness(value)).toBe("degraded");
    expect(channelNeedsAttention(value)).toBe(true);
    expect(channelConnectivityState(value)).toBe("reachable");
  });

  it("derives the account state from the backend verdict and legacy fields", () => {
    expect(channelAccountState(overview())).toBe("unknown");
    expect(
      channelAccountState(
        overview({ account_state: "ok", last_account_probe_ok: true }),
      ),
    ).toBe("ok");
    expect(
      channelAccountState(
        overview({
          account_state: "invalid",
          last_account_probe_error: "upstream_unauthorized",
        }),
      ),
    ).toBe("invalid");
    // Legacy fallback: no account_state, but probe fields exist.
    expect(
      channelAccountState(
        overview({
          last_account_probe_at: "2026-08-09T00:00:00Z",
          last_account_probe_ok: false,
          last_account_probe_error: "account_banned",
        }),
      ),
    ).toBe("banned");
  });

  it("keeps account failure out of business health and readiness", () => {
    const value = overview({
      health_state: "healthy",
      last_probe_ok: true,
      account_state: "invalid",
      last_account_probe_ok: false,
    });
    expect(channelHealthState(value)).toBe("healthy");
    expect(channelReadiness(value)).toBe("ready");
    expect(channelAccountState(value)).toBe("invalid");
  });
});
