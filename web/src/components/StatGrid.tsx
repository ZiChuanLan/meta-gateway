import type { ReactNode } from "react";

export function StatGrid({
	items,
	columns = 3,
}: {
	items: Array<{ label: string; value: ReactNode }>;
	columns?: number;
}) {
	return (
		<div
			className="stat-grid"
			style={{ gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))` }}
		>
			{items.map((item) => (
				<div className="stat-card" key={item.label}>
					<span>{item.label}</span>
					<strong>{item.value}</strong>
				</div>
			))}
		</div>
	);
}
