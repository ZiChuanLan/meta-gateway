import { RefreshCw } from "lucide-react"
import { useQuery, type QueryKey } from "@tanstack/react-query"
import { useState } from "react"
import { api } from "../../api/client"
import { useAdminMutation } from "../../hooks/useAdminMutation"
import { useClientPagination } from "../../hooks/useClientPagination"
import { useI18n } from "../../i18n"
import { useSession } from "../../session"
import { PaginationBar } from "../../components/PaginationBar"
import { Button, DataTable, ErrorState, Loading, Panel, StatusBadge, formatDate } from "../../components/ui"

const DISCOVERY_INVALIDATE_KEYS: QueryKey[] = [
  ["models"],
  ["channels"],
  ["channel-overviews"],
  ["routes"],
  ["route-overviews"],
  ["members"],
  ["explain"],
];

export function DiscoveryPanel() {
  const { client } = useSession();
  const { t } = useI18n();
  const s = api(client!);
  const channels = useQuery({
    queryKey: ["channels"],
    queryFn: ({ signal }) => s.channels(signal),
  });
  const [filter, setFilter] = useState(0);
  const models = useQuery({
    queryKey: ["models", filter],
    queryFn: ({ signal }) => s.discoveredModels(filter || undefined, signal),
  });
  const refresh = useAdminMutation({
    mutationFn: s.refreshAll,
    invalidateKeys: DISCOVERY_INVALIDATE_KEYS,
  });
  const refreshOne = useAdminMutation({
    mutationFn: s.refreshChannel,
    invalidateKeys: DISCOVERY_INVALIDATE_KEYS,
    pendingIdOf: (channelId: number) => channelId,
  });
  const failedChannelIds = new Set(
    (refresh.data?.items ?? [])
      .filter((item) => item.error)
      .map((item) => item.channel_id),
  );
  const modelRows = models.data ?? [];
  const modelPagination = useClientPagination(modelRows, 15);
  const refreshBusy = refresh.isPending || refreshOne.isPending;
  return (
    <Panel
      actions={
        <>
          <select
            aria-label={t("ops.filterChannel")}
            value={filter}
            onChange={(e) => {
              refreshOne.reset();
              setFilter(Number(e.target.value));
            }}
          >
            <option value="0">{t("ops.allChannels")}</option>
            {channels.data?.map((c) => (
              <option value={c.id} key={c.id}>
                {c.name}
              </option>
            ))}
          </select>
          {filter > 0 && (
            <Button
              variant="secondary"
              icon={<RefreshCw size={16} />}
              disabled={refreshBusy}
              onClick={() => {
                refresh.reset();
                refreshOne.mutate(filter);
              }}
            >
              {refreshOne.isPending
                ? t("ops.refreshing")
                : t("ops.refreshChannel")}
            </Button>
          )}
          <Button
            icon={<RefreshCw size={16} />}
            disabled={refreshBusy}
            onClick={() => {
              refreshOne.reset();
              refresh.mutate();
            }}
          >
            {refresh.isPending ? t("ops.refreshing") : t("ops.refreshAll")}
          </Button>
        </>
      }
    >
      {refresh.data && (
        <div className="result-strip">
          <StatusBadge
            value={refresh.data.failure_count > 0 ? "failed" : "success"}
          />
          <span>
            {t("ops.refreshSummary", {
              success: refresh.data.success_count,
              failure: refresh.data.failure_count,
            })}
          </span>
        </div>
      )}
      {refreshOne.data && (
        <div className="result-strip">
          <StatusBadge value="success" />
          <span>
            {t("ops.refreshChannelResult", {
              id: refreshOne.data.channel_id,
              models: refreshOne.data.models.length,
              routes: refreshOne.data.created_routes,
            })}
          </span>
        </div>
      )}
      {refresh.data && failedChannelIds.size > 0 && (
        <div className="result-strip result-strip-error">
          <span>
            {t("ops.refreshFailures", {
              channels: Array.from(failedChannelIds)
                .map((id) => `#${id}`)
                .join(", "),
            })}
          </span>
        </div>
      )}
      {models.isPending ? (
        <Loading />
      ) : models.isError ? (
        <ErrorState error={models.error} />
      ) : (
        <>
          <DataTable
            headers={[
              t("common.model"),
              t("common.channel"),
              t("common.source"),
              t("common.available"),
              t("common.latency"),
              t("common.checked"),
            ]}
            empty={!modelRows.length}
          >
            {modelPagination.pageItems.map((m) => (
              <tr
                key={m.id}
                className={
                  failedChannelIds.has(m.channel_id) ? "row-failed" : undefined
                }
              >
                <td>
                  <strong>{m.model_name}</strong>
                </td>
                <td>#{m.channel_id}</td>
                <td>{m.source}</td>
                <td>
                  <StatusBadge value={m.available} />
                </td>
                <td>{t("common.ms", { n: m.latency_ms })}</td>
                <td>{formatDate(m.checked_at)}</td>
              </tr>
            ))}
          </DataTable>
          <PaginationBar
            page={modelPagination.page}
            totalPages={modelPagination.totalPages}
            total={modelPagination.total}
            pageSize={modelPagination.pageSize}
            rangeStart={modelPagination.rangeStart}
            rangeEnd={modelPagination.rangeEnd}
            hasPrev={modelPagination.hasPrev}
            hasNext={modelPagination.hasNext}
            onPageChange={modelPagination.setPage}
            onPageSizeChange={modelPagination.setPageSize}
          />
        </>
      )}
    </Panel>
  );
}
