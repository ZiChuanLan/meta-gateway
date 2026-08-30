import React, { useEffect, useRef } from "react";

interface SlashParticle {
	x: number;
	y: number;
	vx: number;
	vy: number;
	size: number;
	color: string;
	alpha: number;
	decay: number;
	rotation: number;
	vRot: number;
	lengthRatio: number;
}

interface KatanaCanvasProps {
	charging: boolean;
	chargeProgress: number; // 0 to 1
}

export const KatanaCanvas: React.FC<KatanaCanvasProps> = ({
	charging,
	chargeProgress,
}) => {
	const canvasRef = useRef<HTMLCanvasElement | null>(null);
	const animFrameRef = useRef<number | null>(null);
	const particlesRef = useRef<SlashParticle[]>([]);
	const chargeProgressRef = useRef(chargeProgress);
	const chargingRef = useRef(charging);

	chargeProgressRef.current = chargeProgress;
	chargingRef.current = charging;

	useEffect(() => {
		const canvas = canvasRef.current;
		if (!canvas) return;
		const ctx = canvas.getContext("2d", { alpha: true });
		if (!ctx) return;

		let lastTime = performance.now();

		const resize = () => {
			if (!canvas) return;
			const rect = canvas.getBoundingClientRect();
			const dpr = Math.min(window.devicePixelRatio || 1, 2);
			canvas.width = rect.width * dpr;
			canvas.height = rect.height * dpr;
		};

		window.addEventListener("resize", resize);
		resize();

		const render = (time: number) => {
			lastTime = time;

			const w = canvas.width;
			const h = canvas.height;

			ctx.clearRect(0, 0, w, h);

			const isCharging = chargingRef.current;
			const progress = chargeProgressRef.current;

			// 1. Ambient & Charging Particles Generator
			if (isCharging) {
				const centerX = w * 0.5;
				const centerY = h * 0.5;
				const spawnRate = Math.floor(1 + progress * 3);

				// 星尘：全局稀疏漂浮（不再聚于屏幕中央画圈），随蓄力渐强
				for (let i = 0; i < spawnRate; i++) {
					const theta = Math.random() * Math.PI * 2;
					const radius = Math.min(w, h) * (0.12 + Math.random() * 0.55);
					const px = centerX + Math.cos(theta) * radius;
					const py = centerY + Math.sin(theta) * radius;
					const drift = 0.25 + progress * 0.6;

					particlesRef.current.push({
						x: px,
						y: py,
						vx: Math.cos(theta + Math.PI / 2) * drift + (Math.random() - 0.5) * 0.4,
						vy: Math.sin(theta + Math.PI / 2) * drift + (Math.random() - 0.5) * 0.4,
					size: 0.6 + Math.random() * 1.6,
					color: Math.random() > 0.35 ? "#4a9cd6" : "#b8952a",
					alpha: 0.35 + Math.random() * 0.45,
						decay: 0.003 + Math.random() * 0.006,
						rotation: theta,
						vRot: 0,
						lengthRatio: 1,
					});
				}
			}

			// 2. Active Particle Loop (Rendering & Kinematics)
			const particles = particlesRef.current;
			for (let i = particles.length - 1; i >= 0; i--) {
				const p = particles[i];
				if (!p) continue;
				p.x += p.vx;
				p.y += p.vy;
				p.rotation += p.vRot;
				p.alpha -= p.decay;

				if (p.alpha <= 0 || p.x < -100 || p.x > w + 100 || p.y < -100 || p.y > h + 100) {
					particles.splice(i, 1);
					continue;
				}

				ctx.save();
				ctx.translate(p.x, p.y);
				ctx.rotate(p.rotation);
				ctx.globalAlpha = Math.max(0, p.alpha);

				// Draw Sharp Energy Shard / Particle
				ctx.fillStyle = p.color;
				ctx.shadowBlur = 10;
				ctx.shadowColor = p.color;

				const halfW = p.size * 0.5;
				const halfH = (p.size * p.lengthRatio) * 0.5;

				ctx.beginPath();
				ctx.moveTo(0, -halfH);
				ctx.lineTo(halfW, 0);
				ctx.lineTo(0, halfH);
				ctx.lineTo(-halfW, 0);
				ctx.closePath();
				ctx.fill();

				ctx.restore();
			}

			animFrameRef.current = requestAnimationFrame(render);
		};

		animFrameRef.current = requestAnimationFrame(render);

		return () => {
			window.removeEventListener("resize", resize);
			if (animFrameRef.current) {
				cancelAnimationFrame(animFrameRef.current);
			}
		};
	}, []);

	return (
		<canvas
			ref={canvasRef}
			className="katana-canvas"
			style={{
				position: "absolute",
				inset: 0,
				width: "100%",
				height: "100%",
				pointerEvents: "none",
				zIndex: 2,
			}}
		/>
	);
};
