import { describe, expect, it } from "vitest";

import type { RouteMember, RoutingCandidate } from "../../api/types";
import {
	candidateState,
	getEffectiveRoutingPolicy,
	isActiveCooldown,
	primaryMember,
	sortMembers,
} from "./routingPolicy";

function member(overrides: Partial<RouteMember> & { id: number }): RouteMember {
	return {
		route_id: 1,
		channel_id: overrides.id,
		priority: 10,
		weight: 100,
		enabled: true,
		auto: true,
		manual_override: false,
		fail_count: 0,
		created_at: "",
		updated_at: "",
		...overrides,
	};
}

function candidate(
	id: number,
	over: { priority?: number; weight?: number; enabled?: boolean; cooldown_until?: string; channelStatus?: string },
): RoutingCandidate {
	return {
		member: member({
			id,
			priority: over.priority ?? 10,
			weight: over.weight ?? 100,
			enabled: over.enabled ?? true,
			cooldown_until: over.cooldown_until,
		}),
		credential_usable: true,
		channel: {
			id,
			name: `ch-${id}`,
			base_url: "",
			models_csv: "",
			group_name: "",
			priority: 10,
			weight: 100,
			status: "enabled",
			created_at: "",
			updated_at: "",
		},
	};
}

describe("sortMembers / primaryMember", () => {
	it("orders by priority desc, then weight desc, then name", () => {
		const a = candidate(1, { priority: 10 });
		const b = candidate(2, { priority: 20 });
		const c = candidate(3, { priority: 20, weight: 50 });
		const d = candidate(4, { priority: 20, weight: 50 });
		expect(sortMembers([a, b, c, d]).map((x) => x.member.id)).toEqual([2, 3, 4, 1]);
		expect(primaryMember([a, b])?.member.id).toBe(2);
		expect(primaryMember([])).toBeNull();
	});
});

describe("isActiveCooldown / candidateState", () => {
	it("treats past cooldowns as inactive", () => {
		const past = new Date(Date.now() - 60_000).toISOString();
		const future = new Date(Date.now() + 60_000).toISOString();
		expect(isActiveCooldown({ cooldown_until: past })).toBe(false);
		expect(isActiveCooldown({ cooldown_until: future })).toBe(true);
		expect(isActiveCooldown({})).toBe(false);
	});

	it("derives member state from enabled/cooldown/channel", () => {
		const future = new Date(Date.now() + 60_000).toISOString();
		expect(candidateState(candidate(1, {}))).toBe("ready");
		expect(candidateState(candidate(1, { enabled: false }))).toBe("disabled");
		expect(candidateState(candidate(1, { cooldown_until: future }))).toBe("cooling_down");
	});
});

describe("getEffectiveRoutingPolicy", () => {
	const runtime = { routing_latency_aware: true, routing_error_aware: false };

	it("adaptive forces both signals on", () => {
		expect(getEffectiveRoutingPolicy("adaptive", runtime)).toMatchObject({ latency: true, error: true });
	});

	it("latency mode keeps the runtime error toggle", () => {
		expect(getEffectiveRoutingPolicy("latency", runtime)).toMatchObject({ latency: true, error: false });
	});

	it("returns null without runtime settings", () => {
		expect(getEffectiveRoutingPolicy("adaptive")).toBeNull();
	});
});
