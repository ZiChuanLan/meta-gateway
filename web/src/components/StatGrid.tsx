import type { ReactNode } from "react";
import { useCountUp } from "../hooks/useCountUp";

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

export function StatGrid({
	items,
	columns = 3,
}: {
	items: Array<{
		label: string;
		value: ReactNode;
		/** When set, the card is a toggle filter control. */
		onClick?: () => void;
		active?: boolean;
		hint?: string;
	}>;
	columns?: number;
}) {
	return (
		<div
			className="stat-grid"
			style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}
		>
			{items.map((item) => (
				<button
					type="button"
					key={item.label}
					className={[
						"stat-card",
						item.onClick ? "is-interactive" : "",
						item.active ? "is-active" : "",
					]
						.filter(Boolean)
						.join(" ")}
					onClick={item.onClick}
					disabled={!item.onClick}
					aria-pressed={item.onClick ? Boolean(item.active) : undefined}
					title={item.hint}
				>
					<span>{item.label}</span>
					<strong>
						<StatValue value={item.value} />
					</strong>
				</button>
			))}
		</div>
	);
}
