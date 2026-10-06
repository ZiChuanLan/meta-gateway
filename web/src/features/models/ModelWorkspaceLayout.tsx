import { useState, type ReactNode } from "react";
import { Dialog } from "../../components/ui";
import { useI18n } from "../../i18n";

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
  if (variant === "management")
    return (
      <div className="split models-split">
        {directory}
        <div className="detail-card ops-detail-card is-compact">{detail}</div>
      </div>
    );
  return (
    <div className="model-card-workspace">
      <div
        onClickCapture={(event) => {
          const target = event.target as HTMLElement;
          if (
            target.closest('tbody tr[data-model-selectable="true"]') &&
            !target.closest(".model-price-summary, .bulk-cell, a, input") &&
            (!target.closest("button") || !!target.closest("[data-model-details]"))
          )
            setOpen(true);
        }}
        onKeyUp={(event) => {
          if (
            (event.key === "Enter" || event.key === " ") &&
            event.target instanceof HTMLElement &&
            event.target.tagName === "TR" &&
            event.target.dataset.modelSelectable === "true"
          )
            setOpen(true);
        }}
      >
        {directory}
      </div>
      {open ? (
        <Dialog title={t("modelsPage.cardDetails")} onClose={() => setOpen(false)}>
          <div className="detail-card ops-detail-card is-compact">{detail}</div>
        </Dialog>
      ) : null}
    </div>
  );
}
