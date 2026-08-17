import type { ReactNode } from "react";
import { useCountUp } from "../hooks/useCountUp";
import { useI18n } from "../i18n";

function StatValue({ value }: { value: ReactNode }) {
	if (typeof value === "number") {
		return <RollingNumber value={value} />;
	}
	return <>{value}</>;
}

function RollingNumber({ value }: { value: number }) {
	const display = useCountUp(value);
	return <>{display}</>;
}

type StatTone = "primary" | "info" | "success" | "warning" | "danger";

export type StatItem = {
	label: string;
	value: ReactNode;
	/** When set, the card is a toggle filter control. */
	onClick?: () => void;
	active?: boolean;
	hint?: string;
	/** Optional leading icon chip (lucide icon or similar). */
	icon?: ReactNode;
	/** Optional tone for the icon chip and card accent. */
	tone?: StatTone;
	/** Optional trend delta (0.12 = +12%). Rendered only when not null. */
	trend?: number | null;
};

function TrendBadge({ trend }: { trend: number }) {
	const { t } = useI18n();
	const up = trend >= 0;
	const pct = `${up ? "+" : "−"}${Math.round(Math.abs(trend) * 100)}%`;
	return (
		<span
			className={`stat-trend ${up ? "is-up" : "is-down"}`}
			title={up ? t("dashboard.trendUp") : t("dashboard.trendDown")}
		>
			{up ? "↗" : "↘"} {pct}
		</span>
	);
}

export function StatGrid({
	items,
	columns,
}: {
	items: StatItem[];
	columns?: number;
}) {
	const columnCount = Math.max(
		1,
		Math.min(columns ?? Math.min(items.length, 4), Math.max(items.length, 1)),
	);
	return (
		<div
			className="stat-grid"
			data-count={items.length}
			style={{ "--stat-cols": columnCount } as React.CSSProperties}
		>
			{items.map((item) => (
				<button
					type="button"
					key={item.label}
					className={[
						"stat-card",
						item.onClick ? "is-interactive" : "",
						item.active ? "is-active" : "",
						item.tone ? `tone-${item.tone}` : "",
					]
						.filter(Boolean)
						.join(" ")}
					onClick={item.onClick}
					disabled={!item.onClick}
					aria-pressed={item.onClick ? Boolean(item.active) : undefined}
					title={item.hint}
				>
					<span className="stat-card-top">
						{item.icon ? <span className="stat-icon">{item.icon}</span> : null}
						<span className="stat-label">{item.label}</span>
						{item.trend != null ? <TrendBadge trend={item.trend} /> : null}
					</span>
					<strong>
						<StatValue value={item.value} />
					</strong>
				</button>
			))}
		</div>
	);
}
