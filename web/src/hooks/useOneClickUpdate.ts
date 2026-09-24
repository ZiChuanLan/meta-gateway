import { useCallback, useEffect, useRef, useState } from "react";
import { api } from "../api/client";
import { useSession } from "../session";

export interface UpdateWatch {
	target: string;
	startedAt: number;
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
 */
export function useOneClickUpdate() {
	const { client } = useSession();
	const service = client ? api(client) : null;
	const [watch, setWatch] = useState<UpdateWatch | null>(null);
	const [confirmTarget, setConfirmTarget] = useState<string | null>(null);
	// onDone/onError are stable per mount; refs keep the polling effect from
	// restarting when the caller passes inline closures.
	const onDoneRef = useRef<(() => void) | null>(null);
	const onErrorRef = useRef<((target: string) => void) | null>(null);

	const apply = useCallback(
		async (target: string) => {
			if (!service) return;
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
		const started = Date.now();
		const timer = window.setInterval(async () => {
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
				const target = watch.target;
				setWatch(null);
				onErrorRef.current?.(target);
			}
		}, 3000);
		return () => window.clearInterval(timer);
	}, [watch]);

	return {
		watch,
		confirmTarget,
		setConfirmTarget,
		apply,
		poll,
	};
}
