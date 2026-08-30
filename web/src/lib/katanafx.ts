// 漫画风按钮边缘「磨刀」微火花 —— 原生 DOM + CSS 动画，无第三方依赖。

const SLASH_PALETTE = ["#ffffff", "#eaf6ff", "#7cc4f0", "#bfe3f9", "#d4af37", "#f7e7b0"];

/** 按钮 hover「磨刀」边缘火花 —— 在元素边缘随机位置短暂迸出微小火星。 */
export function createEdgeSparkHost(
	host: HTMLElement,
	palette: readonly string[] = SLASH_PALETTE,
	baseRateMs = 260,
): { start(): void; stop(): void; dispose(): void } {
	let timer: number | null = null;
	let disposed = false;

	const spawn = () => {
		if (disposed || !host.isConnected) return;
		const rect = host.getBoundingClientRect();
		if (rect.width < 4 || rect.height < 4) return;

		const edge = Math.floor(Math.random() * 4);
		const along = Math.random();
		let x: number;
		let y: number;
		let dx: number;
		let dy: number;
		if (edge === 0 || edge === 2) {
			x = rect.left + along * rect.width;
			y = edge === 0 ? rect.top : rect.bottom;
			dy = edge === 0 ? 1 : -1;
			dx = (Math.random() - 0.5) * 1.4;
		} else {
			x = edge === 1 ? rect.right : rect.left;
			y = rect.top + along * rect.height;
			dx = edge === 1 ? 1 : -1;
			dy = (Math.random() - 0.5) * 1.4;
		}

		const color = palette[Math.floor(Math.random() * palette.length)] || "#fff";
		const size = 1.5 + Math.random() * 2.5;
		const spark = document.createElement("span");
		spark.className = "katana-spark";
		spark.style.left = `${x}px`;
		spark.style.top = `${y}px`;
		spark.style.width = `${size}px`;
		spark.style.height = `${size}px`;
		spark.style.background = color;
		spark.style.boxShadow = `0 0 5px ${color}, 0 0 12px ${color}`;
		spark.style.setProperty("--sx", `${(dx + (Math.random() - 0.5) * 1.2) * (10 + Math.random() * 26)}px`);
		spark.style.setProperty("--sy", `${(dy + (Math.random() - 0.5) * 1.2) * (6 + Math.random() * 20)}px`);
		spark.style.setProperty("--sr", `${(Math.random() - 0.5) * 900}deg`);
		spark.style.setProperty("--sd", `${160 + Math.random() * 260}ms`);
		spark.addEventListener("animationend", () => spark.remove(), { once: true });
		document.body.appendChild(spark);

		timer = window.setTimeout(spawn, baseRateMs * (0.45 + Math.random() * 0.8));
	};

	return {
		start() {
			if (timer === null && !disposed) spawn();
		},
		stop() {
			if (timer !== null) {
				window.clearTimeout(timer);
				timer = null;
			}
		},
		dispose() {
			disposed = true;
			this.stop();
		},
	};
}

/** prefers-reduced-motion 快速检查。 */
export function prefersReducedMotion(): boolean {
	return window.matchMedia("(prefers-reduced-motion: reduce)").matches;
}