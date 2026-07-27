import type { ReactNode } from "react";

/**
 * Branded empty / first-run surface. Keep drama lower than the connect page
 * but higher than a blank table message.
 */
export function EmptyHero({
	kicker,
	title,
	body,
	actions,
}: {
	kicker?: string;
	title: string;
	body: string;
	actions?: ReactNode;
}) {
	return (
		<div className="empty-hero">
			<div className="empty-hero-glow" aria-hidden="true" />
			{kicker ? <p className="empty-hero-kicker">{kicker}</p> : null}
			<strong className="empty-hero-title">{title}</strong>
			<p className="empty-hero-body">{body}</p>
			{actions ? <div className="empty-hero-actions">{actions}</div> : null}
		</div>
	);
}
