import { driver, type DriveStep, type Driver } from "driver.js";
import "driver.js/dist/driver.css";
import { useEffect, useRef } from "react";
import { useLocation, useNavigate } from "react-router-dom";
import { useI18n } from "../i18n";
import { useModules } from "../hooks/useModules";

const DISMISS_KEY = "mg.guided-tour.done";

type TourStep = {
  /** Page the target lives on; the tour navigates there on its own. */
  route: string;
  /** Finds the element to spotlight; polled until it appears. */
  locate: () => Element | null;
  /** Runs after navigation, before locating (e.g. open a drawer). */
  prepare?: () => void;
  title: string;
  description: string;
};

function waitFor(step: TourStep, timeout = 8000): Promise<Element | null> {
  return new Promise((resolve) => {
    const started = Date.now();
    const tick = () => {
      const el = step.locate();
      if (el) return resolve(el);
      if (Date.now() - started > timeout) return resolve(null);
      window.setTimeout(tick, 120);
    };
    tick();
  });
}

/**
 * Full-flow spotlight tour (driver.js): crosses pages instead of staying on
 * the dashboard — it navigates to connections, opens the add-connection
 * drawer, walks through keys and logs, spotlighting one element per stop.
 * Runs once per browser on first login (dismissal persists in localStorage);
 * reopen anywhere with ?tour=1.
 *
 * Mounted from the app shell so route changes never unmount the controller.
 */
export function GuidedTour() {
  const { t } = useI18n();
  const navigate = useNavigate();
  const location = useLocation();
  const { checkinEnabled } = useModules();
  const launched = useRef(false);

  useEffect(() => {
    if (launched.current) return;
    // never run under vitest — the overlay buries whatever the test asserts.
    if (import.meta.env.VITEST) return;
    const forced =
      new URLSearchParams(window.location.search).get("tour") === "1";
    if (
      !forced &&
      (localStorage.getItem(DISMISS_KEY) === "1" ||
        location.pathname !== "/")
    ) {
      return;
    }
    // Give the first page a beat to mount so its boxes measure correctly.
    const timer = window.setTimeout(() => {
      launched.current = true;
      start(t, navigate, checkinEnabled);
    }, 600);
    return () => window.clearTimeout(timer);
  }, [location.pathname, t, navigate, checkinEnabled]);

  return null;
}

function start(
  t: (key: string, vars?: Record<string, string | number>) => string,
  navigate: (to: string) => void,
  checkinEnabled: boolean,
) {
  const textButton = (label: string): Element | null =>
    [...document.querySelectorAll("button")].find(
      (button) => (button.textContent ?? "").trim() === label,
    ) ?? null;

  const steps: TourStep[] = [
    {
      route: "/",
      locate: () => document.querySelector(".deck-sector-rail"),
      title: t("tour.navTitle"),
      description: t("tour.navDesc"),
    },
    {
      route: "/",
      locate: () => document.querySelector(".endpoint-strip"),
      title: t("tour.endpointTitle"),
      description: t("tour.endpointDesc"),
    },
    {
      route: "/",
      locate: () => document.querySelector(".setup-guide"),
      title: t("tour.guideTitle"),
      description: t("tour.guideDesc"),
    },
    {
      route: "/channels",
      locate: () => textButton(t("channels.add")),
      title: t("tour.addTitle"),
      description: t("tour.addDesc"),
    },
    {
      route: "/channels",
      prepare: () => textButton(t("channels.add"))?.dispatchEvent(
        new MouseEvent("click", { bubbles: true }),
      ),
      locate: () => document.querySelector(".sync-mode"),
      title: t("tour.syncTitle"),
      description: t("tour.syncDesc"),
    },
    {
      route: "/keys",
      locate: () => textButton(t("keys.create")),
      title: t("tour.keysTitle"),
      description: t("tour.keysDesc"),
    },
    {
      route: "/logs",
      locate: () => document.querySelector(".logs-split"),
      title: t("tour.logsTitle"),
      description: t("tour.logsDesc"),
    },
    {
      route: "/checkins",
      locate: () => document.querySelector(".ops-canvas .panel"),
      title: t("tour.checkinsTitle"),
      description: checkinEnabled
        ? t("tour.checkinsDesc")
        : t("tour.checkinsOffDesc"),
    },
    {
      route: "/settings",
      locate: () => document.getElementById("runtime-relay"),
      title: t("tour.settingsRelayTitle"),
      description: t("tour.settingsRelayDesc"),
    },
    {
      route: "/settings",
      locate: () => document.getElementById("runtime-sync"),
      title: t("tour.settingsSyncTitle"),
      description: t("tour.settingsSyncDesc"),
    },
    {
      route: "/settings",
      locate: () => document.getElementById("runtime-alerts"),
      title: t("tour.settingsAlertsTitle"),
      description: t("tour.settingsAlertsDesc"),
    },
  ];

  const asDriveSteps: DriveStep[] = steps.map((step) => ({
    // driver.js centers the popover on a dummy element when the locator
    // returns null; its types are stricter than its runtime, hence the cast.
    element: () => step.locate() as Element,
    popover: { title: step.title, description: step.description },
  }));

  let index = 0;
  let busy = false;

  const driverObj: Driver = driver({
    animate: true,
    allowClose: true,
    overlayColor: "rgba(15, 42, 66, 0.55)",
    showButtons: ["next", "previous", "close"],
    nextBtnText: t("tour.next"),
    prevBtnText: t("tour.prev"),
    doneBtnText: t("tour.done"),
    showProgress: true,
    progressText: t("tour.progress"),
    steps: asDriveSteps,
    onNextClick: () => advance(1),
    onPrevClick: () => advance(-1),
    onCloseClick: () => driverObj.destroy(),
    onDestroyed: () => localStorage.setItem(DISMISS_KEY, "1"),
  });

  async function advance(dir: 1 | -1) {
    if (busy) return;
    busy = true;
    try {
      let nextIndex = index + dir;
      // The popover moves immediately — spotlighted when the target is
      // already mounted, centered while it mounts. A stop whose target
      // never shows up (e.g. the dashboard checklist is already done or
      // dismissed) is skipped after a short wait, never a dead click.
      while (nextIndex >= 0 && nextIndex < steps.length) {
        const step = steps[nextIndex];
        if (!step) break;
        if (window.location.pathname !== step.route) navigate(step.route);
        step.prepare?.();
        index = nextIndex;
        driverObj.drive(nextIndex);
        if (step.locate()) return;
        const el = await waitFor(step, 3000);
        if (el) {
          driverObj.drive(nextIndex);
          return;
        }
        nextIndex += dir;
      }
      driverObj.destroy();
    } finally {
      busy = false;
    }
  }

  driverObj.drive(0);
}
