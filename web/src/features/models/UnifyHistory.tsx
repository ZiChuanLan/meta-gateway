import { RotateCcw, Undo2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../../api/client";
import { useAdminMutation } from "../../hooks/useAdminMutation";
import { useI18n } from "../../i18n";
import { useSession } from "../../session";
import { Button, Dialog, Empty, ErrorState, formatDate } from "../../components/ui";

const UNIFY_HISTORY_INVALIDATE_KEYS = [
  ["unify-batches"],
  ["unify-preview"],
  ["routes"],
  ["route-overviews"],
  ["members"],
  ["channel-overviews"],
  ["models"],
  ["explain"],
  ["missing-models"],
] as const;

/**
 * Everything the unification assistant has applied, with a way back.
 *
 * Each applied group is a batch: undoing it deletes the alias route and the
 * channel bindings it created and re-enables the originals it archived. A
 * single archived name can also be brought back on its own, leaving the alias
 * and the rest of its batch in place.
 */
export function UnifyHistory({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const { client } = useSession();
  const service = api(client!);

  const history = useQuery({
    queryKey: ["unify-batches"],
    queryFn: ({ signal }) => service.unifyBatches(signal),
  });

  const undo = useAdminMutation({
    mutationFn: (batchId: number) => service.unifyUndo(batchId),
    invalidateKeys: [...UNIFY_HISTORY_INVALIDATE_KEYS],
    toastOnError: false,
  });

  const restore = useAdminMutation({
    mutationFn: (routeId: number) => service.unifyRestoreRoute(routeId),
    invalidateKeys: [...UNIFY_HISTORY_INVALIDATE_KEYS],
    toastOnError: false,
  });

  const batches = history.data?.batches ?? [];
  const archived = history.data?.archived ?? [];
  const pending = undo.isPending || restore.isPending;
  // Per-batch count of names that are still hidden: a restore (or a batch
  // that lost one of its entries) must not keep claiming "隐藏 N 个原名".
  const activeArchivedByBatch = new Map<number, number>();
  for (const entry of archived) {
    if (entry.restored) continue;
    activeArchivedByBatch.set(
      entry.batch_id,
      (activeArchivedByBatch.get(entry.batch_id) ?? 0) + 1,
    );
  }

  return (
    <Dialog title={t("modelsPage.unify.history.title")} onClose={onClose}>
      <p className="unify-intro">{t("modelsPage.unify.history.description")}</p>

      {history.isPending ? (
        <Empty>{t("common.loading")}</Empty>
      ) : history.isError ? (
        <ErrorState error={history.error} />
      ) : batches.length === 0 ? (
        <Empty>{t("modelsPage.unify.history.empty")}</Empty>
      ) : (
        <div className="unify-body">
          {archived.length > 0 ? (
            <section className="unify-section">
              <h3>{t("modelsPage.unify.history.archivedSection")}</h3>
              <p className="unify-section-hint">
                {t("modelsPage.unify.history.archivedHint")}
              </p>
              {archived.map((entry) => (
                <div className="unify-group" key={`${entry.batch_id}:${entry.route_id}`}>
                  <div className="unify-group-head">
                    <strong className="mono">{entry.model_name}</strong>
                    <span className="unify-arrow">→</span>
                    <span className="mono">{entry.canonical}</span>
                    <span className="unify-count">
                      {formatDate(entry.archived_at)}
                    </span>
                    <Button
                      variant="secondary"
                      icon={<RotateCcw size={14} />}
                      disabled={pending}
                      onClick={() => restore.mutate(entry.route_id)}
                    >
                      {t("modelsPage.unify.history.restore")}
                    </Button>
                  </div>
                </div>
              ))}
            </section>
          ) : null}

          <section className="unify-section">
            <h3>{t("modelsPage.unify.history.batchSection")}</h3>
            {batches.map((batch) => (
              <div
                className={`unify-group${batch.undone_at ? " is-off" : ""}`}
                key={batch.id}
              >
                <div className="unify-group-head">
                  <strong className="mono">{batch.canonical}</strong>
                  {batch.undone_at ? (
                    <span className="model-meta-badge">
                      {t("modelsPage.unify.history.undone")}
                    </span>
                  ) : (
                    <span className="model-meta-badge is-mapped">
                      {t("modelsPage.unify.history.active")}
                    </span>
                  )}
                  <span className="unify-count">
                    {t("modelsPage.unify.history.summary", {
                      members: batch.members_created,
                      archived: activeArchivedByBatch.get(batch.id) ?? 0,
                    })}
                  </span>
                  <span className="unify-count">{formatDate(batch.created_at)}</span>
                  {batch.undone_at ? null : (
                    <Button
                      variant="secondary"
                      icon={<Undo2 size={14} />}
                      disabled={pending}
                      onClick={() => undo.mutate(batch.id)}
                    >
                      {t("modelsPage.unify.history.undo")}
                    </Button>
                  )}
                </div>
              </div>
            ))}
          </section>
        </div>
      )}

      {undo.error ? <ErrorState error={undo.error} /> : null}
      {restore.error ? <ErrorState error={restore.error} /> : null}

      <div className="dialog-actions">
        <Button variant="secondary" disabled={pending} onClick={onClose}>
          {t("common.close")}
        </Button>
      </div>
    </Dialog>
  );
}
