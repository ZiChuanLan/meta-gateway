import { DatabaseBackup } from "lucide-react"
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query"
import { api } from "../../api/client"
import { useClientPagination } from "../../hooks/useClientPagination"
import { useI18n } from "../../i18n"
import { useSession } from "../../session"
import { PaginationBar } from "../../components/PaginationBar"
import { Button, DataTable, Empty, ErrorState, Loading, Panel, StatusBadge, formatBytes, formatDate } from "../../components/ui"

export function BackupsPanel() {
  const { client } = useSession();
  const { t } = useI18n();
  const s = api(client!);
  const qc = useQueryClient();
  const q = useQuery({
    queryKey: ["backups"],
    queryFn: ({ signal }) => s.backups(signal),
  });
  const create = useMutation({
    mutationFn: s.createBackup,
    onSuccess: () => qc.invalidateQueries({ queryKey: ["backups"] }),
  });
  const backupRows = q.data ?? [];
  const backupPagination = useClientPagination(backupRows, 12);
  return (
    <Panel
      title={t("ops.backups.title")}
      titleHelp={t("ops.backups.titleHelp")}
      actions={
        <Button
          icon={<DatabaseBackup size={16} />}
          disabled={create.isPending}
          onClick={() => create.mutate()}
        >
          {create.isPending ? t("ops.creatingBackup") : t("ops.createBackup")}
        </Button>
      }
    >
      {create.error && <ErrorState error={create.error} />}
      {create.data && (
        <div className="result-strip">
          <StatusBadge value={create.data.status} />
          <span>
            {t("ops.backupCreated", {
              name: create.data.name,
              size: formatBytes(create.data.size_bytes),
              time: formatDate(create.data.created_at),
            })}
          </span>
        </div>
      )}
      {q.isPending ? (
        <Loading />
      ) : q.isError ? (
        <ErrorState error={q.error} />
      ) : !q.data.length ? (
        <Empty>{t("ops.noBackups")}</Empty>
      ) : (
        <>
          <DataTable
            headers={[
              t("common.name"),
              t("common.status"),
              t("common.size"),
              t("common.duration"),
              t("common.checksum"),
              t("common.created"),
            ]}
          >
            {backupPagination.pageItems.map((b) => (
              <tr key={b.id}>
                <td>
                  <strong>{b.name}</strong>
                </td>
                <td>
                  <StatusBadge value={b.status} />
                </td>
                <td>{formatBytes(b.size_bytes)}</td>
                <td>{t("common.ms", { n: b.duration_ms })}</td>
                <td className="mono truncate">{b.checksum}</td>
                <td>{formatDate(b.created_at)}</td>
              </tr>
            ))}
          </DataTable>
          <PaginationBar
            page={backupPagination.page}
            totalPages={backupPagination.totalPages}
            total={backupPagination.total}
            pageSize={backupPagination.pageSize}
            rangeStart={backupPagination.rangeStart}
            rangeEnd={backupPagination.rangeEnd}
            hasPrev={backupPagination.hasPrev}
            hasNext={backupPagination.hasNext}
            onPageChange={backupPagination.setPage}
            onPageSizeChange={backupPagination.setPageSize}
          />
        </>
      )}
      <p className="muted" style={{ marginTop: 12, fontSize: 12 }}>
        {t("ops.restoreNote", {
          cmd: "meta-gateway restore --from <backup-name>",
        })}
      </p>
    </Panel>
  );
}
