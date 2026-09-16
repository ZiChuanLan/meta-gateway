import { useEffect, useRef, type RefObject } from "react";

type Particle = { x: number; y: number; vx: number; vy: number; size: number; life: number; gold: boolean };

/** The original stardust treatment, now bounded and asleep when not visible. */
export function KatanaCanvas({ charging, chargeProgress, motionTarget }: {
  charging: boolean;
  chargeProgress: number;
  motionTarget?: RefObject<HTMLElement | null>;
}) {
  const canvasRef = useRef<HTMLCanvasElement>(null);
  const charge = useRef({ charging, progress: chargeProgress });
  charge.current = { charging, progress: chargeProgress };

  useEffect(() => {
    const canvas = canvasRef.current;
    if (!canvas || !("CanvasRenderingContext2D" in window)) return;
    const context = canvas.getContext("2d", { alpha: true });
    if (!context) return;
    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)");
    const particles: Particle[] = [];
    const pointer = { x: .45, y: .45, tx: .45, ty: .45 };
    let width = 1, height = 1, frame = 0, previous = 0, emission = 0, energy = 0;
    let dark = document.documentElement.classList.contains("dark");
    let ink = "#7cc4f0", accent = "#d4af37", glowRGB = "124,196,240";
    const readPalette = () => {
      dark = document.documentElement.classList.contains("dark");
      const style = getComputedStyle(document.documentElement);
      ink = style.getPropertyValue("--primary").trim() || "#7cc4f0";
      accent = style.getPropertyValue("--accent").trim() || "#d4af37";
      const hex = /^#([0-9a-f]{6})$/i.exec(ink)?.[1];
      if (hex) glowRGB = [0, 2, 4].map((i) => parseInt(hex.slice(i, i + 2), 16)).join(",");
    };
    const resize = () => {
      const bounds = canvas.getBoundingClientRect();
      width = Math.max(1, bounds.width); height = Math.max(1, bounds.height);
      const dpr = Math.min(window.devicePixelRatio || 1, 1.5, Math.sqrt(3_000_000 / (width * height)));
      canvas.width = Math.round(width * dpr); canvas.height = Math.round(height * dpr);
      context.setTransform(dpr, 0, 0, dpr, 0, 0);
    };
    const onPointer = (event: PointerEvent) => {
      const bounds = canvas.getBoundingClientRect();
      pointer.tx = Math.max(0, Math.min(1, (event.clientX - bounds.left) / width));
      pointer.ty = Math.max(0, Math.min(1, (event.clientY - bounds.top) / height));
    };
    const resetPointer = () => { pointer.tx = .45; pointer.ty = .45; };
    const render = (time: number) => {
      frame = 0;
      if (document.hidden || reduced.matches) return;
      if (previous && time - previous < 32) { frame = requestAnimationFrame(render); return; }
      const dt = Math.min(2, previous ? (time - previous) / 32 : 1);
      previous = time;
      energy += ((charge.current.charging ? charge.current.progress : .12) - energy) * .06;
      pointer.x += (pointer.tx - pointer.x) * .075;
      pointer.y += (pointer.ty - pointer.y) * .075;
      motionTarget?.current?.style.setProperty("--scene-x", String((pointer.x - .5) * 2));
      motionTarget?.current?.style.setProperty("--scene-y", String((pointer.y - .5) * 2));
      context.clearRect(0, 0, width, height);
      const glow = context.createRadialGradient(pointer.x * width, pointer.y * height, 0, pointer.x * width, pointer.y * height, Math.min(width, height) * .6);
      glow.addColorStop(0, `rgba(${glowRGB},${dark ? .065 + energy * .08 : .035 + energy * .055})`);
      glow.addColorStop(1, `rgba(${glowRGB},0)`);
      context.fillStyle = glow; context.fillRect(0, 0, width, height);
      emission += dt;
      const cap = width < 640 ? 36 : 88;
      if (emission > (energy > .2 ? 1 : 5) && particles.length < cap) {
        emission = 0;
        particles.push({ x: Math.random() * width, y: Math.random() * height, vx: .18 + energy * 1.4, vy: -.1 - energy * .55, size: .5 + Math.random() * 1.3, life: .4 + Math.random() * .55, gold: Math.random() < .32 });
      }
      for (let i = particles.length - 1; i >= 0; i--) {
        const p = particles[i]!;
        p.x += p.vx * dt; p.y += p.vy * dt; p.life -= .004 * dt;
        if (p.life <= 0 || p.x > width + 20 || p.y < -20) { particles.splice(i, 1); continue; }
        context.globalAlpha = p.life * (dark ? .75 : .5);
        context.fillStyle = p.gold ? accent : ink;
        context.beginPath();
        context.ellipse(p.x, p.y, p.size * (1 + energy * 3), p.size * .45, -.35, 0, Math.PI * 2);
        context.fill();
      }
      context.globalAlpha = 1;
      frame = requestAnimationFrame(render);
    };
    const sync = () => {
      if (frame) cancelAnimationFrame(frame);
      frame = 0; previous = 0;
      if (reduced.matches) {
        context.clearRect(0, 0, width, height);
        motionTarget?.current?.style.setProperty("--scene-x", "0");
        motionTarget?.current?.style.setProperty("--scene-y", "0");
      } else if (!document.hidden) frame = requestAnimationFrame(render);
    };
    const observer = new ResizeObserver(resize);
    observer.observe(canvas);
    const themeObserver = new MutationObserver(readPalette);
    themeObserver.observe(document.documentElement, { attributes: true, attributeFilter: ["class", "data-appearance"] });
    readPalette(); resize(); sync();
    window.addEventListener("pointermove", onPointer, { passive: true });
    window.addEventListener("blur", resetPointer);
    document.addEventListener("visibilitychange", sync);
    reduced.addEventListener("change", sync);
    return () => {
      if (frame) cancelAnimationFrame(frame);
      observer.disconnect(); themeObserver.disconnect();
      window.removeEventListener("pointermove", onPointer); window.removeEventListener("blur", resetPointer);
      document.removeEventListener("visibilitychange", sync); reduced.removeEventListener("change", sync);
    };
  }, [motionTarget]);

  return <canvas ref={canvasRef} className="katana-canvas" aria-hidden="true" />;
}
