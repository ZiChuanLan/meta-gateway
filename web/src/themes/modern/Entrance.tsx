import { useI18n } from "../../i18n";
import type { EntrancePhase } from "../types";

export function ModernEntrance({ phase }: { phase: EntrancePhase }) {
  const { t } = useI18n();
  return <div className="cinematic-scene">
    <div className="cinematic-field" />
    <div className="cinematic-registration"><span>MG / 01</span><i /><span>WORKSPACE</span></div>
    <div className="cinematic-pane is-left" /><div className="cinematic-pane is-right" />
    <div className="cinematic-core"><span className="cinematic-eyebrow">{t(phase === "sheathing" ? "motion.closing" : "motion.opening")}</span><strong><span>META</span><span>GATEWAY</span></strong><span className="cinematic-caption">{t("motion.caption")}</span><span className="cinematic-charge"><i /></span></div>
    <div className="cinematic-seam" /><div className="cinematic-slash" /><div className="cinematic-trail is-left" /><div className="cinematic-trail is-right" />
  </div>;
}
