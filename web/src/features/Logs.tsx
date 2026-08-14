import { RefreshCw } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useMemo, useEffect, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { AuditPanel, DiscoveryPanel } from "./OpsPanels";
import { api } from "../api/client";
import type { ProxyLog } from "../api/types";
import { EmptyHero } from "../components/EmptyHero";
import { ListShell } from "../components/ListShell";
import { PaginationBar } from "../components/PaginationBar";
import { EntityState } from "../components/EntityState";
import { StatGrid } from "../components/StatGrid";
import { categorizeError } from "../errorCatalog";
import {
  Button,
  DataTable,
  Page,
  Panel,
  StatusBadge,
  Tabs,
  formatDate,
} from "../components/ui";
import { useClientPagination } from "../hooks/useClientPagination";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { formatCost, logCostUsd } from "../lib/format";

// Routing decision audit view: fetched on demand when a log row expands.
function DecisionSnapshotView({ requestId }: { requestId: string }) {
	const { client } = useSession();
	const service = api(client!);
	const { t } = useI18n();
	const snap = useQuery({
		queryKey: ["decision-snapshot", requestId],
		queryFn: ({ signal }) => service.decisionSnapshot(requestId, signal),
		retry: false,
	});
	if (snap.isLoading) {
		return <p className="log-decision-loading">{t("common.working")}</p>;
	}
	const payload = snap.data?.payload;
	if (!payload) {
		return <p className="log-decision-empty">{t("logsPage.decisionEmpty")}</p>;
	}
	const candidates = payload.candidates ?? [];
	const selected = candidates.find((c) => c.eligible)?.candidate?.channel?.name;
	return (
		<div className="log-decision">
			<div className="log-decision-head">
				<strong>{t("logsPage.decisionTitle")}</strong>
				{selected ? (
					<span>
						{t("logsPage.decisionSelected", { channel: selected })}
					</span>
				) : null}
				{payload.routing_mode ? <code>{payload.routing_mode}</code> : null}
				{payload.sticky_hit ? (
					<span className="log-decision-sticky">
						{t("logsPage.decisionStickyHit")}
					</span>
				) : null}
				{payload.sticky_reason ? (
					<span className="log-decision-sticky">
						{t("logsPage.decisionStickyMiss", {
							reason: payload.sticky_reason,
						})}
					</span>
				) : null}
			</div>
			<ul className="log-decision-candidates">
				{candidates.map((candidate, index) => (
					<li
						key={index}
						className={candidate.eligible ? "is-eligible" : ""}
					>
						<span className="log-decision-channel">
							{candidate.candidate?.channel?.name ??
								`#${candidate.candidate?.channel?.id ?? "?"}`}
						</span>
						{candidate.score != null && candidate.score > 0 ? (
							<code>{Math.round(candidate.score)}</code>
						) : null}
						{candidate.eligible ? (
							<span className="log-decision-tag is-ok">
								{t("status.enabled")}
							</span>
						) : (
							<span className="log-decision-tag is-skip">
								{(candidate.reasons ?? []).join(", ") ||
									t("logsPage.decisionSkipped")}
							</span>
						)}
					</li>
				))}
			</ul>
		</div>
	);
}

function ProxyLogsPanel() {
  const { client } = useSession();
  const { t } = useI18n();
  const service = api(client!);
  const [params, setParams] = useSearchParams();
	const channelId = positiveId(params.get("channel_id"));
	const modelParam = params.get("model")?.trim() || "";
	const failedOnly = params.get("status") !== "all";
	const upstreamIdParam = params.get("upstream_request_id")?.trim() || "";
	const [modelDraft, setModelDraft] = useState(modelParam);
	const [upstreamIdDraft, setUpstreamIdDraft] = useState(upstreamIdParam);
	const [slowOnly, setSlowOnly] = useState(false);
	const [expandedRequest, setExpandedRequest] = useState<string | null>(null);
  const [histogram, setHistogram] = useState<{
    buckets: number[];
    total: number;
    slow_count: number;
    p50_ms: number;
    p95_ms: number;
  } | null>(null);

	const filters = useMemo(
		() => ({
			channel_id: channelId,
			model: modelParam || undefined,
			status: failedOnly ? ("failed" as const) : undefined,
			upstream_request_id: upstreamIdParam || undefined,
			limit: 100,
		}),
		[channelId, failedOnly, modelParam, upstreamIdParam],
	);

  const logs = useQuery({
    queryKey: ["proxy-logs", filters],
    queryFn: ({ signal }) => service.proxyLogs(filters, signal),
  });
  // AAH-style latency histogram: load once on mount + on refresh.
  const loadHistogram = () => {
    service
      .proxyLogLatencyHistogram(1000)
      .then(setHistogram)
      .catch(() => undefined);
  };
  useEffect(() => {
    loadHistogram();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const channels = useQuery({
    queryKey: ["channels"],
    queryFn: ({ signal }) => service.channels(signal),
  });
  const channelName = useMemo(() => {
    const map = new Map<number, string>();
    for (const channel of channels.data ?? []) {
      map.set(channel.id, channel.name);
    }
    return map;
  }, [channels.data]);

  // Downstream-key pricing: per-1k prompt/completion unit prices set on the
  // key that issued the request. Used to render a per-log cost column.
  const keys = useQuery({
    queryKey: ["keys"],
    queryFn: ({ signal }) => service.keys(signal),
  });
	const priceMap = useMemo(() => {
		const map = new Map<
			number,
			{ prompt: number; completion: number; cache: number }
		>();
		for (const k of keys.data ?? []) {
			if (k.price_prompt_per_1k || k.price_completion_per_1k || k.price_cache_per_1k) {
				map.set(k.id, {
					prompt: k.price_prompt_per_1k ?? 0,
					completion: k.price_completion_per_1k ?? 0,
					cache: k.price_cache_per_1k ?? 0,
				});
			}
		}
		return map;
	}, [keys.data]);
	const logCost = (log: ProxyLog): number | null => {
		if (log.downstream_key_id == null) return null;
		const price = priceMap.get(log.downstream_key_id);
		if (!price) return null;
		return logCostUsd({
			promptTokens: log.prompt_tokens ?? 0,
			completionTokens: log.completion_tokens ?? 0,
			cacheReadTokens: log.cache_read_tokens ?? 0,
			pricePromptPer1k: price.prompt,
			priceCompletionPer1k: price.completion,
			priceCachePer1k: price.cache,
		});
	};

  const rows = logs.data ?? [];
  const pagination = useClientPagination(
    slowOnly ? rows.filter((log) => log.latency_ms >= 5000) : rows,
    20,
  );
  const pageRows = pagination.pageItems;
  const failedCount = rows.filter((log) => log.status >= 400).length;

  // Friendly, translatable label for a raw backend error category.
  const errorLabel = (raw?: string): string => {
    if (!raw) return "—";
    const cls = categorizeError(raw).class;
    return t(`logsPage.errorClass.${cls}`);
  };

  const setFilter = (patch: Record<string, string | null>) => {
    const next = new URLSearchParams(params);
    for (const [key, value] of Object.entries(patch)) {
      if (value == null || value === "") next.delete(key);
      else next.set(key, value);
    }
    setParams(next, { replace: true });
  };

  return (
    <>
      <div
        className="toolbar"
        style={{ marginBottom: 12, flexWrap: "wrap", gap: 8 }}
      >
        <label className="check" style={{ margin: 0 }}>
          <input
            type="checkbox"
            checked={failedOnly}
            onChange={(e) =>
              setFilter({ status: e.target.checked ? null : "all" })
            }
          />
          <span>{t("logsPage.failedOnly")}</span>
        </label>
        <label className="check" style={{ margin: 0 }}>
          <input
            type="checkbox"
            checked={slowOnly}
            onChange={(e) => setSlowOnly(e.target.checked)}
          />
          <span>{t("logsPage.slowOnly")}</span>
        </label>
        <Button
          variant="secondary"
          icon={<RefreshCw size={16} />}
          onClick={() => {
            void logs.refetch();
          }}
        >
          {t("common.refresh")}
        </Button>
      </div>
      <StatGrid
        items={[
          {
            label: t("logsPage.stat.shown"),
            value: logs.isPending ? "—" : rows.length,
          },
          {
            label: t("logsPage.stat.failed"),
            value: logs.isPending ? "—" : failedCount,
          },
          {
            label: t("logsPage.stat.failRate"),
            value:
              logs.isPending || rows.length === 0
                ? "—"
                : `${Math.round((failedCount / rows.length) * 100)}%`,
          },
        ]}
      />

      {histogram ? (
        <div className="latency-histogram">
          <div className="latency-histogram-head">
            <strong>{t("logsPage.histogram")}</strong>
            <span className="is-quiet">
              {t("logsPage.histogramStats", {
                total: histogram.total,
                slow: histogram.slow_count,
                p50: histogram.p50_ms,
                p95: histogram.p95_ms,
              })}
            </span>
          </div>
          <div
            className={`latency-histogram-bars${histogram.total === 0 ? " is-empty" : ""}`}
          >
            {histogram.buckets.map((count, index) => {
              const max = Math.max(...histogram.buckets, 1);
              const slow = index >= 6; // buckets 6+ = >= 5s
              return (
                <div
                  key={index}
                  className={`latency-histogram-bar${slow ? " is-slow" : ""}`}
                  style={{ height: `${Math.max(4, (count / max) * 100)}%` }}
                  title={`${count} 次`}
                />
              );
            })}
          </div>
          <div className="latency-histogram-labels">
            {["<0.25s", "0.5s", "1s", "2s", "3s", "5s", "8s", "13s", "21s", "34s", "34s+"].map(
              (label, index) => (
                <span key={index}>{label}</span>
              ),
            )}
          </div>
        </div>
      ) : null}

      <div className="logs-split">
        <Panel className="ops-list-panel">
          <div className="filter-bar">
            <select
              aria-label={t("ops.filterChannel")}
              value={channelId ?? 0}
              onChange={(e) => {
                const value = Number(e.target.value);
                setFilter({
                  channel_id: value > 0 ? String(value) : null,
                });
              }}
            >
              <option value={0}>{t("ops.allChannels")}</option>
              {(channels.data ?? []).map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name}
                </option>
              ))}
            </select>
			<input
				value={modelDraft}
				placeholder={t("common.model")}
				onChange={(e) => setModelDraft(e.target.value)}
				onKeyDown={(e) => {
					if (e.key === "Enter") {
						setFilter({ model: modelDraft.trim() || null });
					}
				}}
			/>
			<input
				value={upstreamIdDraft}
				placeholder={t("logsPage.upstreamRequestId")}
				onChange={(e) => setUpstreamIdDraft(e.target.value)}
				onKeyDown={(e) => {
					if (e.key === "Enter") {
						setFilter({ upstream_request_id: upstreamIdDraft.trim() || null });
					}
				}}
			/>
            <Button
              variant="secondary"
              onClick={() => setFilter({ model: modelDraft.trim() || null })}
            >
              {t("common.apply")}
            </Button>
			{(channelId || modelParam || upstreamIdParam || !failedOnly) && (
				<Button
					variant="quiet"
					onClick={() => {
						setModelDraft("");
						setUpstreamIdDraft("");
						setFilter({
							channel_id: null,
							model: null,
							status: null,
							upstream_request_id: null,
						});
					}}
              >
                {t("common.clearFilters")}
              </Button>
            )}
          </div>

          <EntityState
            isLoading={logs.isPending}
            isError={logs.isError}
            error={logs.error}
            isEmpty={!rows.length}
            empty={
              <EmptyHero
                kicker={t("logsPage.emptyKicker")}
                title={t("logsPage.emptyTitle")}
                body={t("logsPage.empty")}
                actions={
                  <>
						<Link className="button button-primary" to="/channels">
							{t("logsPage.ctaChannels")}
						</Link>
                    <Link
                      className="button button-secondary"
                      to="/keys?create=1"
                    >
                      {t("logsPage.ctaKeys")}
                    </Link>
                  </>
                }
              />
            }
            retry={() => logs.refetch()}
          >
            <ListShell
              footer={
                <PaginationBar
                  page={pagination.page}
                  totalPages={pagination.totalPages}
                  total={pagination.total}
                  pageSize={pagination.pageSize}
                  rangeStart={pagination.rangeStart}
                  rangeEnd={pagination.rangeEnd}
                  hasPrev={pagination.hasPrev}
                  hasNext={pagination.hasNext}
                  onPageChange={pagination.setPage}
                  onPageSizeChange={pagination.setPageSize}
                />
              }
            >
              <DataTable
                headers={[
                  t("common.time"),
                  t("common.model"),
                  t("logsPage.reasoningEffort"),
                  t("common.route"),
                  t("common.channel"),
                  t("common.status"),
                  t("common.tokens"),
                  t("common.cacheTokens"),
					t("common.latency"),
					t("common.firstByte"),
					t("common.cost"),
					t("common.clientFamily"),
                ]}
              >
				{pageRows.map((log) => (
					<>
					<tr
						key={log.id}
						className={`${log.status >= 400 ? "row-failed" : ""} log-row-clickable${expandedRequest === log.request_id ? " is-expanded" : ""}`}
						onClick={() =>
							setExpandedRequest((current) =>
								current === log.request_id ? null : log.request_id,
							)
						}
						title={t("logsPage.decisionHint")}
					>
                    <td>{formatDate(log.created_at)}</td>
                    <td>
                      <strong>{log.model}</strong>
                      <small className="mono">{log.request_id}</small>
                    </td>
                    <td>
                      {log.reasoning_effort ? (
                        <code className="log-effort">
                          {log.reasoning_effort}
                        </code>
                      ) : (
                        "—"
                      )}
                    </td>
                    <td>
                      {log.route_id ? (
                        <Link
                          to={`/models?model=${encodeURIComponent(
                            log.route_pattern ?? "",
                          )}`}
                          title={log.route_pattern || undefined}
                        >
                          <code>#{log.route_id}</code>
                        </Link>
                      ) : (
                        "—"
                      )}
                    </td>
                    <td>
                      <Link
                        to={`/channels?id=${log.channel_id}`}
                        title={t("logsPage.openConnection")}
                      >
                        {channelName.get(log.channel_id) ??
                          `#${log.channel_id}`}
                      </Link>
                    </td>
                    <td>
                      <StatusBadge
                        value={
                          log.status >= 400 ? "failed" : String(log.status)
                        }
                      />
                      {log.error_brief ? (
                        <span
                          className="log-error-label"
                          title={log.error_brief}
                        >
                          {errorLabel(log.error_brief)}
                        </span>
                      ) : null}
                      {log.attempt > 1 ? (
                        <span
                          className="log-retry-mark"
                          title={t("logsPage.retried")}
                        >
                          {t("logsPage.retried")}
                        </span>
                      ) : null}
                    </td>
                    <td>{log.total_tokens ? log.total_tokens : "—"}</td>
                    <td>
                      {log.cache_read_tokens || log.cache_creation_tokens
                        ? `${log.cache_read_tokens ?? 0} / ${log.cache_creation_tokens ?? 0}`
                        : "—"}
                    </td>
					<td>{t("common.ms", { n: log.latency_ms })}</td>
					<td>
						{log.stream && log.first_byte_ms
							? t("common.ms", { n: log.first_byte_ms })
							: "—"}
					</td>
					<td className="log-cost">
						{logCost(log) != null ? formatCost(logCost(log)!) : "—"}
					</td>
					<td>{log.client_family || "—"}</td>
					</tr>
					{expandedRequest === log.request_id ? (
						<tr className="log-decision-row" key={`${log.id}-decision`}>
							<td colSpan={12}>
								<DecisionSnapshotView requestId={log.request_id} />
							</td>
						</tr>
					) : null}
					</>
				))}
              </DataTable>
            </ListShell>
          </EntityState>
        </Panel>
      </div>
    </>
  );
}

export function Logs() {
  const { t } = useI18n();
  const [params, setParams] = useSearchParams();
  const rawTab = params.get("tab");
  const tabItems = [
    { value: "proxy", label: t("logsPage.tab.proxy") },
    { value: "discovery", label: t("logsPage.tab.discovery") },
    { value: "audit", label: t("logsPage.tab.audit") },
  ];
  const active = tabItems.some((item) => item.value === rawTab)
    ? (rawTab as string)
    : "proxy";

  const changeTab = (value: string) => {
    const next = new URLSearchParams(params);
    if (value === "proxy") next.delete("tab");
    else next.set("tab", value);
    // Keep channel/model filters only on proxy tab.
    if (value !== "proxy") {
      next.delete("channel_id");
      next.delete("model");
      next.delete("status");
    }
    setParams(next, { replace: true });
  };

  return (
    <Page
      kicker={t("logsPage.kicker")}
      title={t("logsPage.title")}
      description={t("logsPage.hubDescription")}
    >
      <div className="ops-canvas">
        <Tabs items={tabItems} active={active} onChange={changeTab} />
        {active === "proxy" ? <ProxyLogsPanel /> : null}
        {active === "discovery" ? <DiscoveryPanel /> : null}
        {active === "audit" ? <AuditPanel /> : null}
      </div>
    </Page>
  );
}

function positiveId(value: string | null) {
  if (!value) return undefined;
  const parsed = Number(value);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined;
}
