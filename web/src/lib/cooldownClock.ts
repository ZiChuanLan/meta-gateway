import { useEffect, useState } from "react";

/**
 * Soonest deadline still in the future, as epoch ms; null when every entry is
 * blank, malformed, or already elapsed.
 */
export function nextCooldownDeadline(
	deadlines: readonly (string | null | undefined)[],
	now = Date.now(),
): number | null {
	let soonest: number | null = null;
	for (const iso of deadlines) {
		if (!iso) continue;
		const at = new Date(iso).getTime();
		if (!Number.isFinite(at) || at <= now) continue;
		if (soonest === null || at < soonest) soonest = at;
	}
	return soonest;
}

/**
 * Re-renders the caller when the cooldowns currently on screen elapse.
 *
 * Cooldown verdicts are derived at render time (`cooldown_until > Date.now()`),
 * so a row whose penalty has just expired keeps rendering "cooling down":
 * nothing React knows about changed, so nothing re-renders, and the row only
 * recovers on the next poll or page switch. Arming a single timer per expiry
 * flips every affected row at once and costs nothing while no cooldown is
 * pending — deliberately not a one-second tick over a whole member list.
 *
 * Pass the `cooldown_until` values of the rows being rendered.
 */
export function useCooldownExpiry(
	deadlines: readonly (string | null | undefined)[],
): void {
	// The clock is sampled into state so the timer callback can re-arm the
	// hook; the verdict itself is always read from a fresh Date.now() in render.
	const [now, setNow] = useState(() => Date.now());
	const soonest = nextCooldownDeadline(deadlines, now);
	useEffect(() => {
		if (soonest === null) return;
		// +250ms absorbs timer jitter: firing early would re-render with the row
		// still cooling down, re-arm the same deadline and stall there.
		const id = window.setTimeout(
			() => setNow(Date.now()),
			Math.max(0, soonest - Date.now()) + 250,
		);
		return () => window.clearTimeout(id);
	}, [soonest]);
}
