import type { ReactNode } from "react";
import { ArrowDownRight, ArrowUpRight } from "lucide-react";
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
	onClick?: () => void;
	active?: boolean;
	hint?: string;
	icon?: ReactNode;
	tone?: StatTone;
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
			{up ? <ArrowUpRight size={11} /> : <ArrowDownRight size={11} />} {pct}
		</span>
	);
}

/**
 * 战术遥测矩阵舱 (Tactical Telemetry Pods):
 * 1px 发丝网格一体化相连，大号 Mono 读数，编号刻度与状态指示点。
 */
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
			className="telemetry-grid"
			data-count={items.length}
			style={{ "--telemetry-cols": columnCount } as React.CSSProperties}
		>
			{items.map((item, idx) => {
				const numTag = String(idx + 1).padStart(2, "0");
				return (
					<button
						type="button"
						key={item.label}
						className={[
							"telemetry-pod",
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
						<div className="telemetry-pod-header">
							<span className="telemetry-index">[{numTag}]</span>
							{item.icon ? <span className="telemetry-icon">{item.icon}</span> : null}
							<span className="telemetry-label">{item.label}</span>
							{item.trend != null ? <TrendBadge trend={item.trend} /> : null}
						</div>
						<div className="telemetry-pod-body">
							<strong className="telemetry-value">
								<StatValue value={item.value} />
							</strong>
							<span className="telemetry-indicator" aria-hidden="true" />
						</div>
					</button>
				);
			})}
		</div>
	);
}