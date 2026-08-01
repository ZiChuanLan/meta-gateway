import { useEffect, useRef, useState } from "react";

/**
 * Animates a number from 0 to `value` when it enters the viewport.
 * Used by stat cards to give the console a "live gauge" feel without
 * distracting re-animation on every re-render.
 */
export function useCountUp(value: number, duration = 700) {
	const [display, setDisplay] = useState(0);
	const started = useRef(false);
	const [reduceMotion, setReduceMotion] = useState(false);

	useEffect(() => {
		if (typeof window === "undefined" || !window.matchMedia) {
			setReduceMotion(true);
			return;
		}
		const query = window.matchMedia("(prefers-reduced-motion: reduce)");
		setReduceMotion(query.matches);
		const onChange = (event: MediaQueryListEvent) =>
			setReduceMotion(event.matches);
		query.addEventListener?.("change", onChange);
		return () => query.removeEventListener?.("change", onChange);
	}, []);

	useEffect(() => {
		if (reduceMotion) {
			setDisplay(value);
			return;
		}
		if (started.current) {
			// Subsequent updates snap to the new value (no re-play).
			setDisplay(value);
			return;
		}
		started.current = true;
		let raf = 0;
		const t0 = performance.now();
		const tick = (now: number) => {
			const progress = Math.min(1, (now - t0) / duration);
			// easeOutCubic for a fast-start / settle landing.
			const eased = 1 - Math.pow(1 - progress, 3);
			setDisplay(Math.round(eased * value));
			if (progress < 1) raf = requestAnimationFrame(tick);
			else setDisplay(value);
		};
		raf = requestAnimationFrame(tick);
		return () => cancelAnimationFrame(raf);
	}, [value, duration, reduceMotion]);

	return display;
}
