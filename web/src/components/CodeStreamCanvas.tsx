import React, { useEffect, useRef } from "react";
import { prefersReducedMotion } from "../lib/katanafx";

interface StreamLine {
	y: number;
	x: number;
	speed: number;
	alpha: number;
	text: string;
	color: string;
}

// 黑客风「代码流」背景：终端日志行 + 符号流，从右向左水平流动。
// 低透明度、低对比，作为登录页氛围层存在，不抢前景。
const CHARS = "01<>/{}[]$#+*=!?:;~^&%|λΔΨΞ·0123456789abcdefABCDEF";

const LOG_LINES = [
	"> POST /v1/chat/completions 200 12ms",
	"> route.select(model: gpt-4o, site: 3)",
	"> GET /admin/sites 200 4ms",
	"> relay failover -> site-2 0x7F3A",
	"> audit.write(request_id=ok)",
	"> models: 128 · latency 42ms",
	"> key pool rotate [ok]",
	"> session token signed 4096",
	"> healthz 200 uptime 99.98%",
	"> route(retry=2, cooldown=30s)",
	"> provider.trace 0x1F → exchange",
	"> checkin cron 0 8 * * * [idle]",
	"> discovery sweep 24 sites",
	"> cache hit ratio 0.93",
	"> metric relay.p95=118ms",
] as const;

function makeLine(w: number, h: number): StreamLine {
	const useLog = Math.random() > 0.45;
	let text: string;
	if (useLog) {
		text = LOG_LINES[Math.floor(Math.random() * LOG_LINES.length)] as string;
	} else {
		let s = "";
		const len = 14 + Math.floor(Math.random() * 26);
		for (let i = 0; i < len; i++) {
			s += CHARS[Math.floor(Math.random() * CHARS.length)];
		}
		text = s;
	}
	return {
		y: h * (0.06 + Math.random() * 0.72),
		x: w + 40 + Math.random() * w * 0.4,
		speed: 0.5 + Math.random() * 1.5,
		alpha: 0.1 + Math.random() * 0.3,
		text,
		color: Math.random() > 0.7 ? "79, 132, 178" : "60, 110, 155",
	};
}

export const CodeStreamCanvas: React.FC = () => {
	const canvasRef = useRef<HTMLCanvasElement | null>(null);

	useEffect(() => {
		const canvas = canvasRef.current;
		if (!canvas) return;
		if (prefersReducedMotion()) return;
		const ctx = canvas.getContext("2d");
		if (!ctx) return;

		const dpr = Math.min(window.devicePixelRatio || 1, 2);
		let w = 0;
		let h = 0;
		const lines: StreamLine[] = [];

		const resize = () => {
			w = window.innerWidth;
			h = window.innerHeight;
			canvas.width = Math.max(1, Math.round(w * dpr));
			canvas.height = Math.max(1, Math.round(h * dpr));
		};
		resize();
		window.addEventListener("resize", resize);

		// 开场预铺几行，避免空白
		for (let i = 0; i < 10; i++) lines.push(makeLine(w, h));

		let raf = 0;
		let lastSpawn = 0;

		const frame = (t: number) => {
			ctx.clearRect(0, 0, canvas.width, canvas.height);
			ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
			ctx.font = "11px var(--mono), monospace";
			ctx.textBaseline = "middle";

			if (t - lastSpawn > 150) {
				lastSpawn = t;
				lines.push(makeLine(w, h));
				if (lines.length > 26) lines.shift();
			}

			for (let i = lines.length - 1; i >= 0; i--) {
				const l = lines[i]!;
				l.x -= l.speed;
				if (l.x + l.text.length * 7 < -80) {
					lines.splice(i, 1);
					continue;
				}
				// 尾部渐隐
				const fade = Math.min(1, (w - l.x + 120) / 200);
				ctx.globalAlpha = l.alpha * Math.max(0, fade);
				ctx.fillStyle = `rgba(${l.color}, 1)`;
				ctx.fillText(l.text, l.x, l.y);
			}
			ctx.globalAlpha = 1;

			raf = requestAnimationFrame(frame);
		};
		raf = requestAnimationFrame(frame);

		return () => {
			cancelAnimationFrame(raf);
			window.removeEventListener("resize", resize);
		};
	}, []);

	return (
		<canvas
			ref={canvasRef}
			className="code-stream-canvas"
			style={{
				position: "absolute",
				inset: 0,
				width: "100%",
				height: "100%",
				pointerEvents: "none",
				zIndex: 0,
				opacity: 0.5,
			}}
			aria-hidden="true"
		/>
	);
};