import { useEffect, useRef, useState } from "react";

/** Decorative light paths, deliberately separate from request telemetry. */
export function DashboardAura() {
  const rootRef = useRef<HTMLDivElement>(null);
  const [paused, setPaused] = useState(true);
  useEffect(() => {
    const root = rootRef.current;
    if (!root) return;
    let visible = true;
    const sync = () => setPaused(document.hidden || !visible);
    const observer = typeof IntersectionObserver === "undefined" ? null : new IntersectionObserver(([entry]) => {
      visible = entry?.isIntersecting ?? false;
      sync();
    });
    observer?.observe(root);
    document.addEventListener("visibilitychange", sync);
    sync();
    return () => { observer?.disconnect(); document.removeEventListener("visibilitychange", sync); };
  }, []);
  return <div ref={rootRef} className={`dashboard-aura${paused ? " is-paused" : ""}`} aria-hidden="true">
    <svg viewBox="0 0 560 220" fill="none">
      <g className="aura-gate">
        <path d="M335-20H395L291 220H231Z" fill="currentColor" opacity=".07" />
        <path className="aura-plane" d="M381-20H423L319 220H277Z" fill="currentColor" opacity=".5" />
        <path className="aura-plane is-second" d="M438-20H458L354 220H334Z" fill="currentColor" opacity=".18" />
      </g>
    </svg>
  </div>;
}
