import { act, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { nextCooldownDeadline, useCooldownExpiry } from "./cooldownClock";

describe("nextCooldownDeadline", () => {
	const now = Date.parse("2026-09-19T00:00:00Z");
	const at = (offsetMs: number) => new Date(now + offsetMs).toISOString();

	it("picks the soonest deadline still in the future", () => {
		expect(nextCooldownDeadline([at(60_000), at(5_000), at(30_000)], now)).toBe(
			now + 5_000,
		);
	});

	it("ignores blank, malformed and elapsed entries", () => {
		expect(
			nextCooldownDeadline(
				[undefined, null, "", "not-a-date", at(-1)],
				now,
			),
		).toBeNull();
		expect(nextCooldownDeadline([], now)).toBeNull();
	});

	it("treats a deadline landing exactly on now as elapsed", () => {
		expect(nextCooldownDeadline([at(0)], now)).toBeNull();
	});
});

describe("useCooldownExpiry", () => {
	beforeEach(() => vi.useFakeTimers());
	afterEach(() => vi.useRealTimers());

	it("re-renders at the deadline and then goes quiet", () => {
		vi.setSystemTime(new Date("2026-09-19T00:00:00Z"));
		const until = new Date(Date.now() + 5_000).toISOString();
		let renders = 0;
		renderHook(() => {
			renders += 1;
			useCooldownExpiry([until]);
		});
		expect(renders).toBe(1);

		// Edge-triggered, not a one-second tick: nothing fires before the
		// deadline, so idle pages do not re-render the member list per second.
		act(() => {
			vi.advanceTimersByTime(3_000);
		});
		expect(renders).toBe(1);

		// The row now observes a fresh clock and flips out of "cooling down".
		act(() => {
			vi.advanceTimersByTime(3_000);
		});
		expect(renders).toBe(2);

		act(() => {
			vi.advanceTimersByTime(600_000);
		});
		expect(renders).toBe(2);
	});

	it("advances to the next pending deadline after the earliest elapses", () => {
		vi.setSystemTime(new Date("2026-09-19T00:00:00Z"));
		const near = new Date(Date.now() + 2_000).toISOString();
		const far = new Date(Date.now() + 10_000).toISOString();
		let renders = 0;
		renderHook(() => {
			renders += 1;
			useCooldownExpiry([near, far]);
		});

		act(() => {
			vi.advanceTimersByTime(2_500);
		});
		expect(renders).toBe(2);

		act(() => {
			vi.advanceTimersByTime(8_000);
		});
		expect(renders).toBe(3);

		act(() => {
			vi.advanceTimersByTime(600_000);
		});
		expect(renders).toBe(3);
	});

	it("does not arm a timer when every cooldown has already elapsed", () => {
		vi.setSystemTime(new Date("2026-09-19T00:00:00Z"));
		const past = new Date(Date.now() - 60_000).toISOString();
		let renders = 0;
		renderHook(() => {
			renders += 1;
			useCooldownExpiry([past]);
		});
		act(() => {
			vi.advanceTimersByTime(600_000);
		});
		expect(renders).toBe(1);
		expect(vi.getTimerCount()).toBe(0);
	});
});
