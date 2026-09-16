import { SkipForward } from "lucide-react";
import { useEffect, useRef, useState, type CSSProperties } from "react";
import { createPortal } from "react-dom";
import { useI18n } from "../i18n";
import { ENTRANCE_CHARGE_MS, ENTRANCE_EXIT_MS, ENTRANCE_REVEAL_MS } from "../lib/entranceMotion";
import { useModalFocus } from "./overlayFocus";
import { useUITheme } from "../appearance";
import { UI_THEMES } from "../themes/registry";
import type { UIThemeId } from "../themes/types";

type EntrancePhase = "sealing" | "revealing" | "sheathing";

/** Visual layer only: authentication and replay each own their completion. */
export function GatewayTransition({ phase, onSkip, appearance = "modern" }: { phase: EntrancePhase; onSkip: () => void; appearance?: UIThemeId }) {
  const { t } = useI18n();
  const rootRef = useRef<HTMLDivElement>(null);
  useModalFocus(rootRef, onSkip);
  const Entrance = UI_THEMES[appearance].Entrance;
  const style = {
    "--entry-charge": `${ENTRANCE_CHARGE_MS}ms`,
    "--entry-reveal": `${ENTRANCE_REVEAL_MS}ms`,
    "--entry-exit": `${ENTRANCE_EXIT_MS}ms`,
  } as CSSProperties;
  return createPortal(
    <div ref={rootRef} tabIndex={-1} role="dialog" aria-modal="true" aria-label={t("motion.entrance")}
      className={`gateway-transition gateway-${appearance === "classic" ? "classic" : "cinematic"} is-${phase}`} style={style}>
      <div className="transition-art" aria-hidden="true"><Entrance phase={phase} /></div>
      <button type="button" className="cinematic-skip" onClick={onSkip}><SkipForward size={14} />{t("motion.skip")}<kbd>Esc</kbd></button>
    </div>, document.body,
  );
}

/** Replaying the entrance never reads or changes the user's session. */
export function GatewayPreview({ onClose }: { onClose: () => void }) {
  const appearance = useUITheme();
  const [phase, setPhase] = useState<EntrancePhase>("sealing");
  const closeRef = useRef(onClose);
  closeRef.current = onClose;
  useEffect(() => {
    const reduced = window.matchMedia("(prefers-reduced-motion: reduce)");
    const charge = window.setTimeout(() => setPhase("revealing"), reduced.matches ? 0 : ENTRANCE_CHARGE_MS);
    const finish = window.setTimeout(() => closeRef.current(), reduced.matches ? 160 : ENTRANCE_CHARGE_MS + ENTRANCE_REVEAL_MS);
    const preferenceChanged = () => { if (reduced.matches) closeRef.current(); };
    reduced.addEventListener("change", preferenceChanged);
    return () => { window.clearTimeout(charge); window.clearTimeout(finish); reduced.removeEventListener("change", preferenceChanged); };
  }, []);
  return <GatewayTransition appearance={appearance} phase={phase} onSkip={onClose} />;
}
