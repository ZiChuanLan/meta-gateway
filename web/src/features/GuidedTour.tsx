import { driver, type DriveStep } from "driver.js";
import "driver.js/dist/driver.css";
import { useEffect, useRef } from "react";
import { useLocation } from "react-router-dom";
import { useI18n } from "../i18n";

const DISMISS_KEY = "mg.guided-tour.done";

/**
 * Spotlight tour (driver.js): dims the console and walks the operator
 * through the nav rail, the API endpoint strip, the setup checklist and the
 * telemetry band with next/previous popovers. Runs once per browser (the
 * dismissal persists in localStorage); reopen with ?tour=1.
 *
 * Mounted on the dashboard because every tour target is either global chrome
 * (the nav rail) or lives on this page.
 */
export function GuidedTour() {
  const { t } = useI18n();
  const location = useLocation();
  const launched = useRef(false);

  useEffect(() => {
    if (location.pathname !== "/" || launched.current) return;
    const forced =
      new URLSearchParams(window.location.search).get("tour") === "1";
    if (!forced && localStorage.getItem(DISMISS_KEY) === "1") return;

    // Give the dashboard panels a beat to mount so their boxes measure.
    const timer = window.setTimeout(() => {
      launched.current = true;
      const steps: DriveStep[] = [];
      const push = (selector: string, title: string, description: string) => {
        if (document.querySelector(selector)) {
          steps.push({ element: selector, popover: { title, description } });
        }
      };
      push(".deck-sector-rail", t("tour.navTitle"), t("tour.navDesc"));
      push(".endpoint-strip", t("tour.endpointTitle"), t("tour.endpointDesc"));
      push(".setup-guide", t("tour.guideTitle"), t("tour.guideDesc"));
      push(".telemetry-stack", t("tour.telemetryTitle"), t("tour.telemetryDesc"));
      if (steps.length === 0) return;

      const driverObj = driver({
        animate: true,
        allowClose: true,
        overlayColor: "rgba(15, 42, 66, 0.55)",
        showButtons: ["next", "previous", "close"],
        nextBtnText: t("tour.next"),
        prevBtnText: t("tour.prev"),
        doneBtnText: t("tour.done"),
        showProgress: true,
        progressText: t("tour.progress"),
        steps,
        onDestroyed: () => localStorage.setItem(DISMISS_KEY, "1"),
      });
      driverObj.drive();
    }, 500);
    return () => window.clearTimeout(timer);
  }, [location.pathname, t]);

  return null;
}
