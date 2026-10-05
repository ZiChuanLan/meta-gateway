import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "../api/client";
import { useSession } from "../session";

export interface UpdateWatch {
	target: string;
	startedAt: number;
}

export interface UpdateFailure {
	target: string;
	/**
	 * The updater's own reason, verbatim, when it reported one. Empty means the
	 * watch simply ran out of time and nothing was ever confirmed.
	 */
	reason: string;
}

/**
 * Drives the one-click container update: apply, then watch the PUBLIC health
 * endpoint until it reports the target version.
 *
 * Why /healthz and not /admin/self-update: the handoff tears the current
 * container down the moment the successor is healthy, so the admin API answer
 * "which phase?" stops being trustworthy mid-flight. The public endpoint is
 * answered by whichever container currently owns the port — when it reports the
 * target version, the successor has taken over.
 *
 * The 3s poll and the 3-minute budget match the ops settings panel, which used
 * this flow first; both panels now share this hook so their behavior cannot
 * drift.
 *
 * The admin status is polled too, but for the opposite reason: a handoff that
 * fails fails FAST (a socket it cannot open, an image it cannot pull) and
 * /admin/self-update is the only place that says why. Waiting the full three
 * minutes and then reporting "not confirmed in time" hid a permission error
 * behind a timeout for an operator who had simply not been told.
 */
export function useOneClickUpdate() {
	const { client } = useSession();
	const service = client ? api(client) : null;
	const [failure, setFailure] = useState<UpdateFailure | null>(null);
	const [watch, setWatch] = useState<UpdateWatch | null>(null);
	const [confirmTarget, setConfirmTarget] = useState<string | null>(null);
	// `api(client)` builds a fresh object every render, so it must not be an effect
	// dependency: that would tear down and restart the 3s poll on every render and
	// the tick would never land.
	const serviceRef = useRef(service);
	serviceRef.current = service;
	// onDone/onError are stable per mount; refs keep the polling effect from
	// restarting when the caller passes inline closures.
	const onDoneRef = useRef<(() => void) | null>(null);
	const onErrorRef = useRef<((target: string) => void) | null>(null);

	const apply = useCallback(
		async (target: string) => {
			if (!service) return;
			setFailure(null);
			await service.applySelfUpdate(target);
			setConfirmTarget(null);
			setWatch({ target, startedAt: Date.now() });
		},
		[service],
	);

	const poll = useCallback(
		(watching: UpdateWatch, onDone: () => void, onError: (target: string) => void) => {
			onDoneRef.current = onDone;
			onErrorRef.current = onError;
			setWatch(watching);
		},
		[],
	);

	useEffect(() => {
		if (!watch) return;
		const service = serviceRef.current;
		if (!service) return;
		const started = Date.now();
		const timer = window.setInterval(async () => {
			// A failed handoff reports itself; take that over the timeout, because
			// it is the difference between "unknown" and "here is what to fix".
			try {
				const status = await service.selfUpdateStatus();
				if (status.phase === "failed" && status.error) {
					setWatch(null);
					setFailure({ target: watch.target, reason: status.error });
					onErrorRef.current?.(watch.target);
					return;
				}
			} catch {
				// Mid-handoff the admin API is legitimately unreachable; the public
				// endpoint below is what decides success.
			}
			try {
				const res = await fetch("/healthz");
				const body = (await res.json()) as { version?: string };
				if (body.version === watch.target) {
					setWatch(null);
					onDoneRef.current?.();
					return;
				}
			} catch {
				// Container restarting — keep polling.
			}
			if (Date.now() - started > 180_000) {
				setWatch(null);
				setFailure({ target: watch.target, reason: "" });
				onErrorRef.current?.(watch.target);
			}
		}, 3000);
		return () => window.clearInterval(timer);
	}, [watch]);

	return {
		watch,
		failure,
		// Kept for callers that only need "which target failed".
		failedTarget: failure?.target ?? null,
		confirmTarget,
		setConfirmTarget,
		apply,
		poll,
	};
}
