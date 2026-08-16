import { ShieldCheck } from "lucide-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { useState } from "react"
import { api } from "../../api/client"
import { useClientPagination } from "../../hooks/useClientPagination"
import { useI18n } from "../../i18n"
import { useSession } from "../../session"
import { PaginationBar } from "../../components/PaginationBar"
import { Button, ConfirmDialog, DataTable, ErrorState, Loading, Panel, StatusBadge, formatDate } from "../../components/ui"

export function AuditPanel() {
  const { client } = useSession();
  const { t } = useI18n();
  const s = api(client!);
  const qc = useQueryClient();
  const [before, setBefore] = useState<number | undefined>();
  const [confirm, setConfirm] = useState(false);
  const q = useQuery({
    queryKey: ["audit", before],
    queryFn: ({ signal }) => s.auditEvents(before, signal),
  });
  const cleanup = useMutation({
    mutationFn: s.cleanupAudit,
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ["audit"] });
      setConfirm(false);
    },
  });
  const auditRows = q.data ?? [];
  const auditPagination = useClientPagination(auditRows, 15);
  return (
    <Panel
      actions={
        <Button
          variant="secondary"
          icon={<ShieldCheck size={16} />}
          onClick={() => setConfirm(true)}
        >
          {t("ops.applyRetention")}
        </Button>
      }
    >
      {q.isPending ? (
        <Loading />
      ) : q.isError ? (
        <ErrorState error={q.error} />
      ) : (
        <>
          <DataTable
            headers={[
              t("common.action"),
              t("common.actor"),
              t("common.resource"),
              t("common.outcome"),
              t("common.status"),
              t("common.category"),
              t("common.time"),
            ]}
            empty={!auditRows.length}
          >
            {auditPagination.pageItems.map((e) => (
              <tr key={e.id}>
                <td>
                  <strong>{e.action}</strong>
                  <small>#{e.id}</small>
                </td>
                <td>{e.actor_kind}</td>
                <td>
                  {e.resource_kind || "-"}
                  {e.resource_id ? ` #${e.resource_id}` : ""}
                </td>
                <td>
                  <StatusBadge value={e.outcome} />
                </td>
                <td>{e.status_code}</td>
                <td>{e.category || "-"}</td>
                <td>{formatDate(e.created_at)}</td>
              </tr>
            ))}
          </DataTable>
          <PaginationBar
            page={auditPagination.page}
            totalPages={auditPagination.totalPages}
            total={auditPagination.total}
            pageSize={auditPagination.pageSize}
            rangeStart={auditPagination.rangeStart}
            rangeEnd={auditPagination.rangeEnd}
            hasPrev={auditPagination.hasPrev}
            hasNext={auditPagination.hasNext}
            onPageChange={auditPagination.setPage}
            onPageSizeChange={auditPagination.setPageSize}
          />
          {q.data.length === 100 && (
            <Button
              variant="secondary"
              onClick={() => setBefore(q.data.at(-1)?.id)}
            >
              {t("ops.olderEvents")}
            </Button>
          )}
          {before && (
            <Button variant="quiet" onClick={() => setBefore(undefined)}>
              {t("ops.newestEvents")}
            </Button>
          )}
        </>
      )}
      {confirm && (
        <ConfirmDialog
          title={t("ops.applyRetentionTitle")}
          message={t("ops.applyRetentionMsg")}
          confirmLabel={t("ops.runCleanup")}
          pending={cleanup.isPending}
          error={cleanup.error}
          onClose={() => setConfirm(false)}
          onConfirm={() => cleanup.mutate()}
        />
      )}
    </Panel>
  );
}
