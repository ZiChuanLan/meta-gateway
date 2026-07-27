import type { ReactNode } from "react";
import { StatusBadge } from "./ui";

/**
 * Entity-bound success/failure strip. Keep results next to the acted-on entity.
 */
export function ResultStrip({
	tone = "info",
	status,
	children,
}: {
	tone?: "info" | "error" | "success";
	/** @deprecated Prefer `tone`. Kept for call sites that pass semantic status. */
	status?: string | boolean;
	children: ReactNode;
}) {
	const resolvedTone: "info" | "error" | "success" =
		tone !== "info"
			? tone
			: status === "error" || status === "failed"
				? "error"
				: status === "success"
					? "success"
					: "info";
	const className = [
		"result-strip",
		resolvedTone === "error"
			? "result-strip-error"
			: resolvedTone === "success"
				? "result-strip-success"
				: "result-strip-info",
	].join(" ");
	const badgeValue =
		status !== undefined
			? status
			: resolvedTone === "info"
				? undefined
				: resolvedTone;
	return (
		<div className={className} role="status">
			{badgeValue !== undefined ? <StatusBadge value={badgeValue} /> : null}
			{typeof children === "string" || typeof children === "number" ? (
				<span>{children}</span>
			) : (
				children
			)}
		</div>
	);
}
