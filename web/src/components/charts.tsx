import { useMemo, useRef, useState } from "react";
import { useI18n } from "../i18n";
import { formatTokens } from "../lib/format";

/**
 * Hand-rolled SVG hourly traffic chart — no chart dependency.
 * Primary series: requests per hour (bars). Secondary: token usage (line).
 */

/**
 * Tooltip center position as a percentage of chart width.
 * Clamped so the tip never hangs off the card edges even when it is
 * wider than half a slot (edge bars would otherwise overflow the panel).
 */
function chartTipLeft(index: number, count: number) {
	const pct = (index / Math.max(1, count)) * 100 + 100 / Math.max(1, count) / 2;
	return Math.min(Math.max(pct, 14), 86);
}

/** Compact axis label: 0, 250, 1.5k, 12k … */
function compactNumber(value: number) {
	if (value >= 1000) {
		const k = value / 1000;
		return `${k >= 100 ? Math.round(k) : k.toFixed(k >= 10 ? 0 : 1)}k`;
	}
	return String(Math.round(value));
}

export function HourlyTrafficChart({
	requests,
	tokens,
	labels,
	height = 168,
	selected = null,
	onSelect,
	labelStep = 4,
	zoomed = false,
}: {
	/** One value per hour, oldest → newest, aligned to local wall-clock hours. */
	requests: number[];
	/** Parallel token values for the same buckets (may be all zero). */
	tokens: number[];
	/** Short display label per hour (e.g. "14:00"). Same length as requests. */
	labels: string[];
	height?: number;
	/** Bucket index to render as selected (drill-down target). */
	selected?: number | null;
	/** Called with the bucket index when a bar is clicked. */
	onSelect?: (index: number) => void;
	/** Label one tick every N buckets (4 for 24h, 8 for 48h). */
	labelStep?: number;
	/** Render the drill-down view with a zoom transition. */
	zoomed?: boolean;
}) {
	const [hot, setHot] = useState<number | null>(null);
	const wrapRef = useRef<HTMLDivElement | null>(null);
	const { t } = useI18n();

	const PAD_L = 34;
	const PAD_B = 20;
	const PAD_T = 8;

	const { bars, linePath, areaPath, yTicks } = useMemo(() => {
		const W = 720;
		const H = height;
		const n = requests.length;
		const maxReq = Math.max(1, ...requests);
		const maxTok = Math.max(1, ...tokens);
		const slot = W / Math.max(1, n);
		const barW = Math.max(3, slot * 0.62);
		const plotH = H - PAD_B - PAD_T;
		const bars = requests.map((v, i) => ({
			x: PAD_L + i * slot + (slot - barW) / 2,
			w: barW,
			h: (v / maxReq) * plotH,
			y: H - PAD_B - (v / maxReq) * plotH,
		}));
		const pts = tokens.map((v, i) => {
			const x = PAD_L + i * slot + slot / 2;
			const y = H - PAD_B - (v / maxTok) * plotH;
			return [x, y] as const;
		});
		const linePath =
			pts.length > 1
				? pts
						.map(([x, y], i) => `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`)
						.join(" ")
				: "";
		const areaPath =
			linePath && pts.length > 1
				? (() => {
						const last = pts[pts.length - 1];
						const first = pts[0];
						if (!last || !first) return "";
						return `${linePath} L${last[0].toFixed(1)},${H - PAD_B} L${first[0].toFixed(1)},${H - PAD_B} Z`;
					})()
				: "";
		// Y-axis labels at 0 / mid / max of the request series.
		const yTicks = [1, 0.5, 0].map((f) => ({
			f,
			label: f === 0 ? "0" : compactNumber(maxReq * f),
			y: H - PAD_B - f * plotH,
		}));
		return { bars, linePath, areaPath, yTicks };
	}, [requests, tokens, height]);

	const hasData = requests.some((v) => v > 0) || tokens.some((v) => v > 0);
	const hotBar = hot != null && hot < bars.length ? bars[hot] : null;
	const hotLabel = hot != null && hot < labels.length ? labels[hot] : null;

	/** Map a pointer/touch X (viewport px) to the bar index under it. */
	const indexFromX = (clientX: number) => {
		const rect = wrapRef.current?.getBoundingClientRect();
		if (!rect || rect.width === 0) return null;
		const x = ((clientX - rect.left) / rect.width) * 720 - PAD_L;
		const i = Math.floor(x / (720 / Math.max(1, bars.length)));
		return i >= 0 && i < bars.length ? i : null;
	};

	return (
		<div className={`chart-block${zoomed ? " is-zoomed" : ""}`}>
			{hasData ? (
				<div className="chart-legend">
					<span className="chart-legend-item">
						<i className="chart-legend-bar" aria-hidden="true" />
						{t("dashboard.chartLegendRequests")}
					</span>
					<span className="chart-legend-item">
						<i className="chart-legend-line" aria-hidden="true" />
						{t("dashboard.chartLegendTokens")}
					</span>
				</div>
			) : null}
			<div
				ref={wrapRef}
				className={`chart-wrap${zoomed ? " is-zoomed" : ""}`}
				style={{ height }}
				onMouseLeave={() => setHot(null)}
				onTouchStart={(e) => {
					const i = indexFromX(e.touches[0]?.clientX ?? 0);
					if (i != null) setHot(i);
				}}
				onTouchMove={(e) => {
					const i = indexFromX(e.touches[0]?.clientX ?? 0);
					if (i != null) setHot(i);
				}}
				onTouchEnd={() => setHot(null)}
			>
				{!hasData ? (
					<p className="dashboard-empty chart-empty">{t("dashboard.chartEmpty")}</p>
				) : (
					<svg
						viewBox={`0 0 720 ${height}`}
						width="100%"
						height={height}
						role="img"
						aria-label="hourly requests and tokens"
						preserveAspectRatio="none"
					>
						<defs>
							<linearGradient id="chart-area-fill" x1="0" y1="0" x2="0" y2="1">
								<stop offset="0%" stopColor="var(--accent)" stopOpacity="0.22" />
								<stop offset="100%" stopColor="var(--accent)" stopOpacity="0.02" />
							</linearGradient>
						</defs>
						{/* Y-axis labels */}
						{yTicks.map((tick) => (
							<text
								key={tick.f}
								x={PAD_L - 7}
								y={tick.y + 3}
								textAnchor="end"
								className="chart-tick chart-tick-y"
							>
								{tick.label}
							</text>
						))}
						{/* gridlines */}
						{[0.25, 0.5, 0.75].map((f) => (
							<line
								key={f}
								x1={PAD_L}
								x2="720"
								y1={height - PAD_B - (height - PAD_B - PAD_T) * f}
								y2={height - PAD_B - (height - PAD_B - PAD_T) * f}
								className="chart-gridline"
							/>
						))}
						{areaPath ? <path d={areaPath} fill="url(#chart-area-fill)" /> : null}
						{linePath ? <path d={linePath} className="chart-token-line" /> : null}
						{bars.map((b, i) => (
							<rect
								key={i}
								x={b.x}
								y={b.y}
								width={b.w}
								height={b.h}
								rx={2}
								className={
									hot === i
										? "chart-bar is-hot"
										: selected === i
											? "chart-bar is-selected"
											: "chart-bar"
								}
								onMouseEnter={() => setHot(i)}
								onClick={() => onSelect?.(i)}
							/>
						))}
						{/* bucket ticks every labelStep buckets */}
						{labels.map((label, i) =>
							i % labelStep === 0 ? (
								<text
									key={i}
									x={PAD_L + i * (720 / labels.length) + 720 / labels.length / 2}
									y={height - 5}
									textAnchor="middle"
									className="chart-tick"
								>
									{label}
								</text>
							) : null,
						)}
					</svg>
				)}
				{hot != null && hotBar && hotLabel ? (
					<div
						className="chart-tip"
						style={{
							left: `${chartTipLeft(hot, bars.length)}%`,
						}}
					>
						<strong>{hotLabel}</strong>
						<span>
							{t("dashboard.chartTooltip", {
								req: String(requests[hot] ?? 0),
								tok: formatTokens(tokens[hot] ?? 0),
							})}
						</span>
					</div>
				) : null}
			</div>
		</div>
	);
}
