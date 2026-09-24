import { describe, expect, it } from "vitest";

import type { Channel, RouteMember, RoutingCandidate } from "../../api/types";
import {
	candidateState,
	getEffectiveRoutingPolicy,
	isActiveCooldown,
	primaryMember,
	sortMembers,
	upstreamChoices,
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
	over: {
		priority?: number;
		weight?: number;
		enabled?: boolean;
		cooldown_until?: string;
		channelStatus?: Channel["status"];
		channelId?: number;
		channelName?: string;
		group?: string;
		mappingJson?: string;
	},
): RoutingCandidate {
	return {
		member: member({
			id,
			priority: over.priority ?? 10,
			weight: over.weight ?? 100,
			enabled: over.enabled ?? true,
			cooldown_until: over.cooldown_until,
			channel_id: over.channelId ?? id,
			group_name: over.group,
			mapping_json: over.mappingJson,
		}),
		credential_usable: true,
		channel: {
			id: over.channelId ?? id,
			name: over.channelName ?? `ch-${id}`,
			base_url: "",
			models_csv: "",
			group_name: "",
			priority: 10,
			weight: 100,
			status: over.channelStatus ?? "enabled",
			created_at: "",
			updated_at: "",
		},
	};
}

describe("sortMembers / primaryMember", () => {
  it("shows the pinned member even when another member has higher priority", () => {
    const automatic = candidate(1, { priority: 100 });
    const pinned = candidate(2, { priority: 1, enabled: false });
    expect(primaryMember([automatic, pinned], { routing_mode: "single", single_member_id: 2 })).toBe(pinned);
    expect(primaryMember([automatic, pinned], { routing_mode: "single", single_member_id: 99 })).toBe(automatic);
  });
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

describe("upstreamChoices", () => {
	// Stands in for useI18n's t: the only key this helper formats.
	const t = (key: string, vars?: Record<string, string | number>) =>
		key === "routing.memberOrigin" ? `原模型 ${vars?.model}` : key;

	// The bug this covers: one channel serving a shared alias under several
	// upstream names printed the SAME row N times ("商汤日日新 · p0/w100"), so the
	// picker could neither tell the rows apart nor say what each one reaches.
	it("names the 原模型 behind a shared alias, per member", () => {
		const choices = upstreamChoices(
			[
				candidate(1, { channelId: 7, channelName: "商汤日日新" }),
				candidate(2, {
					channelId: 7,
					channelName: "商汤日日新",
					mappingJson: '{"real":"SenseNova-V6"}',
				}),
			],
			undefined,
			t,
		);
		expect(choices.map((choice) => choice.label)).toEqual([
			"商汤日日新 · p10/w100",
			"商汤日日新 · 原模型 SenseNova-V6 · p10/w100",
		]);
		// The value is the MEMBER: a channel id cannot address the second row.
		expect(choices.map((choice) => choice.memberId)).toEqual([1, 2]);
	});

	it("falls back to the route-level alias mapping", () => {
		const route = { mapping_json: '{"real":"legacy-name"}' };
		const choices = upstreamChoices([candidate(1, { channelName: "dav" })], route, t);
		expect(choices.map((choice) => choice.label)).toEqual([
			"dav · 原模型 legacy-name · p10/w100",
		]);
	});

	it("keeps same-named channels apart by channel id", () => {
		const choices = upstreamChoices(
			[
				candidate(1, { channelId: 7, channelName: "商汤日日新" }),
				candidate(2, { channelId: 9, channelName: "商汤日日新" }),
			],
			undefined,
			t,
		);
		expect(choices.map((choice) => choice.label)).toEqual([
			"商汤日日新 · p10/w100 · #7",
			"商汤日日新 · p10/w100 · #9",
		]);
	});

	it("falls back to the group when the channel is the same one", () => {
		const choices = upstreamChoices(
			[
				candidate(1, { channelId: 7, channelName: "商汤日日新", group: "vip" }),
				candidate(2, { channelId: 7, channelName: "商汤日日新" }),
			],
			undefined,
			t,
		);
		expect(choices.map((choice) => choice.label)).toEqual([
			"商汤日日新 · p10/w100 · #7 · vip",
			"商汤日日新 · p10/w100 · #7 · default",
		]);
	});

	it("leaves an unambiguous list alone", () => {
		const choices = upstreamChoices(
			[
				candidate(1, { channelName: "a", priority: 5 }),
				candidate(2, { channelName: "b", priority: 20 }),
			],
			undefined,
			t,
		);
		// Sorted like the model page: priority desc.
		expect(choices.map((choice) => choice.label)).toEqual([
			"b · p20/w100",
			"a · p5/w100",
		]);
	});
});
