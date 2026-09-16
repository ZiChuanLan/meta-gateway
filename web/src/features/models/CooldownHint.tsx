import { useEffect, useState } from "react"
import { useI18n } from "../../i18n"
import { formatCooldownLeft } from "./routingPolicy"

export /** Cooldown countdown that re-renders itself every second until expiry. */
function CooldownHint({ until }: { until: string }) {
  const { t } = useI18n();
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const deadline = new Date(until).getTime();
    const tick = () => {
      const current = Date.now();
      setNow(current);
      if (!Number.isFinite(deadline) || current >= deadline) window.clearInterval(id);
    };
    const id = deadline > Date.now() ? window.setInterval(tick, 1000) : undefined;
    const immediate = window.setTimeout(tick, 0);
    return () => {
      window.clearInterval(id);
      window.clearTimeout(immediate);
    };
  }, [until]);
  const remaining = new Date(until).getTime() - now;
  if (!Number.isFinite(remaining) || remaining <= 0) return null;
  return (
    <span className="member-cooldown-hint">
      {t("routing.cooldownHint", { left: formatCooldownLeft(until, now) })}
    </span>
  );
}
