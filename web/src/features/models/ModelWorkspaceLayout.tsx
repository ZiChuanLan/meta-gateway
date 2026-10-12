import { useEffect, useState, type KeyboardEvent, type MouseEvent, type ReactNode } from "react";
import { Button, Dialog } from "../../components/ui";
import { useI18n } from "../../i18n";

/**
 * The console's own mobile layer (the same 760px `workspaces.css` keys off).
 * Below it a two-column split cannot hold a directory and a detail side by
 * side, so the management page becomes the list plus a dialog.
 */
const MOBILE_QUERY = "(max-width: 760px)";

/**
 * Reports whether the phone breakpoint is active. Tolerant of environments
 * without `matchMedia` (SSR, a bare jsdom): an unavailable query reads as
 * desktop, which is the split view's default rather than a mobile fallback.
 */
function useIsMobile() {
  const [mobile, setMobile] = useState(
    () =>
      typeof window !== "undefined" &&
      typeof window.matchMedia === "function" &&
      window.matchMedia(MOBILE_QUERY).matches,
  );
  useEffect(() => {
    if (typeof window === "undefined" || typeof window.matchMedia !== "function") return;
    const media = window.matchMedia(MOBILE_QUERY);
    const onChange = (event: MediaQueryListEvent) => setMobile(event.matches);
    setMobile(media.matches);
    media.addEventListener("change", onChange);
    return () => media.removeEventListener("change", onChange);
  }, []);
  return mobile;
}

/**
 * "Which click opens a model?" — one answer, shared by the member cards and the
 * phone management list so the two cannot drift: a selectable row opens, but
 * its price/bulk/link/input cells and plain buttons do not. Inside the action
 * cell only an explicit `data-model-details` button counts as an open.
 */
function rowOpenHandlers(setOpen: (value: boolean) => void) {
  return {
    onClickCapture: (event: MouseEvent<HTMLDivElement>) => {
      const target = event.target as HTMLElement;
      if (
        target.closest('[data-model-selectable="true"]') &&
        !target.closest(".model-price-summary, .bulk-cell, a, input") &&
        (!target.closest("button") || !!target.closest("[data-model-details]"))
      )
        setOpen(true);
    },
    onKeyUp: (event: KeyboardEvent<HTMLDivElement>) => {
      if (
        (event.key === "Enter" || event.key === " ") &&
        event.target instanceof HTMLElement &&
        event.target.tagName === "TR" &&
        event.target.dataset.modelSelectable === "true"
      )
        setOpen(true);
    },
  };
}

/** Management defaults to a persistent split view; only members opt into cards. */
export function ModelWorkspaceLayout({
  directory,
  detail,
  variant = "management",
}: {
  directory: ReactNode;
  detail: ReactNode;
  variant?: "management" | "cards";
}) {
  const [open, setOpen] = useState(false);
  const { t } = useI18n();
  const mobile = useIsMobile();
  // Members always read the detail through the dialog; management only does so
  // on a phone. One `detail` instance exists either way.
  const usesDialog = variant === "cards" || mobile;
  const { onClickCapture, onKeyUp } = rowOpenHandlers(setOpen);

  // Crossing the breakpoint drops the dialog: a phone opens the list closed
  // even with a route already selected, and back on desktop the detail is the
  // persistent pane again. The directory is not part of this state, so a resize
  // never remounts it and never loses its filter or scroll.
  useEffect(() => {
    setOpen(false);
  }, [mobile]);

  return (
    <>
      <div
        className={variant === "management" ? "split models-split" : "model-card-workspace"}
        onClickCapture={usesDialog ? onClickCapture : undefined}
        onKeyUp={usesDialog ? onKeyUp : undefined}
      >
        {directory}
        {variant === "management" && !mobile ? (
          <div className="detail-card ops-detail-card is-compact">{detail}</div>
        ) : null}
      </div>
      {usesDialog && open ? (
        <Dialog
          title={t("modelsPage.cardDetails")}
          onClose={() => setOpen(false)}
          actions={
            variant === "management" ? (
              <Button variant="secondary" onClick={() => setOpen(false)}>
                {t("modelsPage.backToDirectory")}
              </Button>
            ) : undefined
          }
        >
          <div
            className={
              variant === "management"
                ? "detail-card ops-detail-card is-compact model-mobile-detail"
                : "detail-card ops-detail-card is-compact"
            }
          >
            {detail}
          </div>
        </Dialog>
      ) : null}
    </>
  );
}
