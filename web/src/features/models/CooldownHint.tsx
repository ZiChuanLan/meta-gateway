import { useEffect, useState } from "react"
import { useI18n } from "../../i18n"
import { formatCooldownLeft } from "./routingPolicy"

export /** Cooldown countdown that re-renders itself every second until expiry. */
function CooldownHint({ until }: { until: string }) {
  const { t } = useI18n();
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (new Date(until).getTime() - Date.now() <= 0) return;
    const id = window.setInterval(() => setNow(Date.now()), 1000);
    return () => window.clearInterval(id);
  }, [until]);
  const remaining = new Date(until).getTime() - now;
  if (remaining <= 0) return null;
  return (
    <span className="member-cooldown-hint">
      {t("routing.cooldownHint", { left: formatCooldownLeft(until, now) })}
    </span>
  );
}
