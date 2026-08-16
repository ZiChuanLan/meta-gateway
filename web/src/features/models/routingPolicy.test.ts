import { describe, expect, it } from "vitest";

import type { FinanceItem, RouteMember, RoutingCandidate } from "../../api/types";
import {
	candidateState,
	getEffectiveRoutingPolicy,
	isActiveCooldown,
	memberPriceUsd,
	primaryMember,
	sortMembers,
	sortMembersByPrice,
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

describe("memberPriceUsd / sortMembersByPrice", () => {
	const items: FinanceItem[] = [
		{ channel_id: 1, balance: 0, quota_per_unit: 2, prices: { "gpt-x": { model: "gpt-x", price_usd: 0.5 } } },
		{ channel_id: 2, balance: 0, quota_per_unit: 2, prices: { "gpt-x": { model: "gpt-x", price_usd: 2 } } },
	];

	it("computes per-1M price from the finance table", () => {
		expect(memberPriceUsd(member({ id: 1, channel_id: 1 }), "gpt-x", items)).toBe(0.5);
		expect(memberPriceUsd(member({ id: 9, channel_id: 9 }), "gpt-x", items)).toBeNull();
		expect(memberPriceUsd(member({ id: 1, channel_id: 1 }), "unknown-model", items)).toBeNull();
	});

	it("sorts cheapest first, unpriced sink to the bottom", () => {
		const pricey = candidate(2, {});
		const cheap = candidate(1, {});
		const unpriced = candidate(7, {});
		expect(sortMembersByPrice([pricey, unpriced, cheap], "gpt-x", items).map((x) => x.member.id)).toEqual([1, 2, 7]);
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
