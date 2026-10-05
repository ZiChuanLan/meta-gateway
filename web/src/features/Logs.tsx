import {
  RefreshCw,
  MessagesSquare,
  SlidersHorizontal,
  Timer,
  Download,
} from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { Fragment, useMemo, useEffect, useState, type ReactNode } from "react";
import { Link, useSearchParams } from "react-router-dom";
import { AuditPanel, DiscoveryPanel } from "./ops";
import { api } from "../api/client";
import type { ProxyLog } from "../api/types";
import { EmptyHero } from "../components/EmptyHero";
import { ListShell } from "../components/ListShell";
import { PaginationBar } from "../components/PaginationBar";
import { EntityState } from "../components/EntityState";
import { TelemetryStrip } from "../components/TelemetryStrip";
import { TimeRangePicker, useUrlTimeRange } from "../components/TimeRangePicker";
import { categorizeError } from "../errorCatalog";
import {
  Button,
  DataTable,
  ErrorState,
  Page,
  Panel,
  StatusBadge,
  Tabs,
  formatDate,
} from "../components/ui";
import { useClientPagination } from "../hooks/useClientPagination";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import {
  ADMIN_LOG_CAPS,
  MEMBER_LOG_CAPS,
  type LogsCapabilities,
  type LogsSource,
} from "./logs/LogsSource";
import { memberLogsSource } from "../member/MemberLogsSource";
import { formatCost } from "../lib/format";
import { positiveId } from "../lib/positiveId"
import { downloadText, timestampedName, toCSV } from "../lib/csv";
import { LiveTracePanel } from "./LiveTracePanel";

/** Latency bucket labels mirror store.LatencyBucketBounds (ms upper bounds). */
const HISTOGRAM_LABELS = [
  "<0.25s",
  "0.5s",
  "1s",
  "2s",
  "3s",
  "5s",
  "8s",
  "13s",
  "21s",
  "34s",
  "34s+",
];

/** Buckets from index 6 up are >= 5s (the slow threshold). */
const HISTOGRAM_SLOW_FROM = 6;

/**
 * Rows pulled for the distribution. An explicit window asks for a large sample
 * so a busy hour is not judged by its newest thousand requests; "all time"
 * stays small because it has no upper bound to narrow the scan.
 */
function histogramSample(bounded: boolean): number {
  return bounded ? 20000 : 1000;
}


// Routing decision audit view: fetched on demand when a log row expands.
// Each attempt row shows the snapshot of ITS OWN selection (matched by
// attempt number); requests whose snapshots predate per-attempt storage fall
// back to the latest one.
function DecisionSnapshotView({
	requestId,
	attempt,
}: {
	requestId: string;
	attempt: number;
}) {
	const { client } = useSession();
	const service = api(client!);
	const { t } = useI18n();
	const snap = useQuery({
		queryKey: ["decision-snapshot", requestId, attempt],
		queryFn: ({ signal }) =>
			service.decisionSnapshot(requestId, attempt, signal),
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
	const selectedID = snap.data?.selected_channel_id;
	// "Served by" must name the channel this attempt actually picked, not the
	// highest-priority eligible candidate.
	const selected =
		candidates.find((c) => c.candidate?.channel?.id === selectedID)
			?.candidate?.channel?.name ??
		candidates.find((c) => c.eligible)?.candidate?.channel?.name;
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
				{candidates.map((candidate, index) => {
					const isPicked =
						candidate.candidate?.channel?.id != null &&
						candidate.candidate.channel.id === selectedID;
					const cooling = (candidate.reasons ?? []).includes(
						"cooling_down",
					);
					return (
						<li
							key={index}
							className={`${candidate.eligible ? "is-eligible" : "is-skipped"}${isPicked ? " is-picked" : ""}`}
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
								<span
									className={`log-decision-tag is-skip${cooling ? " is-cooling" : ""}`}
								>
									{(candidate.reasons ?? []).join(", ") ||
										t("logsPage.decisionSkipped")}
								</span>
							)}
						</li>
					);
				})}
			</ul>
		</div>
	);
}

/**
 * Latency bar fill as a fraction of a 0–10s scale, floored at 6% so a fast
 * attempt still draws a visible nub instead of an empty track.
 */
function latencyShare(latencyMs: number): number {
	return Math.min(1, Math.max(0.06, latencyMs / 10000));
}

/** One hop of the chain; `to` turns the value into a link to its owner. */
function ChainStep({ label, value, note, to }: {
	label: string;
	value: ReactNode;
	note?: ReactNode;
	to?: string;
}) {
	return (
		<div className="log-chain-step">
			<span className="log-chain-label">{label}</span>
			{to ? (
				<Link className="log-chain-value" to={to}>
					{value}
				</Link>
			) : (
				<span className="log-chain-value">{value}</span>
			)}
			{note ? <span className="log-chain-note">{note}</span> : null}
		</div>
	);
}

/**
 * The request chain, reconstructed after the fact: client token → ingress path
 * → route → channel → upstream URL. Every hop links to the object that owns it,
 * because "which token was this and where did it go" is the first question
 * about a row the operator does not recognise.
 */
function LogChain({ log, channelName, keyName }: {
	log: ProxyLog;
	channelName: Map<number, string>;
	keyName: Map<number, string>;
}) {
	const { t } = useI18n();
	const knownTokenName = log.downstream_key_id
		? keyName.get(log.downstream_key_id)
		: undefined;
	const tokenLabel = knownTokenName ??
		(log.downstream_key_id ? `#${log.downstream_key_id}` : null);
	const ingressNote = [
		log.client_family,
		log.stream ? t("logsPage.chainStream") : null,
	]
		.filter(Boolean)
		.join(" · ");
	// proxy_logs.path stores the endpoint relative to the API root (the relay's
	// own name for it, e.g. "chat/completions"); the client's ingress path has
	// the /v1 prefix the relay is mounted under.
	const ingressPath = log.path
		? log.path.startsWith("/")
			? log.path
			: `/v1/${log.path}`
		: "—";
	// The upstream key is stored as an id (the secret itself never is), so the
	// display name comes from the server-side join. Rows written before that
	// column existed carry only the fingerprint — say so rather than show a
	// blank hop, which would read as "no key was used".
	const keyID = log.upstream_key_id ?? 0;
	const keyLabel = keyID
		? log.upstream_key_name || `#${keyID}`
		: log.key_fingerprint || "—";
	const keyNote = keyID
		? log.upstream_key_name
			? `#${keyID}`
			: undefined
		: log.key_fingerprint
			? t("logsPage.chainKeyFingerprintOnly")
			: undefined;
	const steps: Array<{
		label: string;
		value: ReactNode;
		note?: ReactNode;
		to?: string;
	}> = [
		{
			label: t("logsPage.token"),
			value: tokenLabel ?? t("logsPage.chainNoToken"),
			// The id only rides along when the name carried it; otherwise the value
			// IS the id and repeating it in the note reads as two facts.
			note: knownTokenName ? `#${log.downstream_key_id}` : undefined,
			// The Keys page has no per-row detail view; its search box is the
			// only way to land on one token, so the hop links there by name.
			to: knownTokenName
				? `/keys?search=${encodeURIComponent(knownTokenName)}`
				: undefined,
		},
		{
			label: t("logsPage.chainIngress"),
			value: <code>{ingressPath}</code>,
			note: ingressNote || undefined,
		},
		{
			label: t("common.route"),
			value: log.route_id ? `#${log.route_id}` : "—",
			note: log.route_pattern || t("logsPage.chainUnrouted"),
			to: log.route_id
				? `/models?model=${encodeURIComponent(log.route_pattern ?? "")}`
				: undefined,
		},
		{
			label: t("common.channel"),
			value: channelName.get(log.channel_id) ?? `#${log.channel_id}`,
			to: `/channels?id=${log.channel_id}`,
		},
		{
			label: t("logsPage.chainKey"),
			value: keyLabel,
			note: keyNote,
			// Straight to the key list this name came from (?keys= opens that
			// channel's key drawer on the connections page).
			to: keyID ? `/channels?keys=${log.channel_id}` : undefined,
		},
		{
			label: t("logsPage.chainUpstream"),
			value: (
				<code title={log.upstream_url || undefined}>
					{log.upstream_url || "—"}
				</code>
			),
			note: t("logsPage.chainAttempt", { n: log.attempt }),
		},
	];
	return (
		<div className="log-chain">
			<strong className="log-chain-head">{t("logsPage.chainTitle")}</strong>
			<div className="log-chain-steps">
				{steps.map((step, index) => (
					<Fragment key={step.label}>
						{index > 0 ? (
							<span className="log-chain-arrow" aria-hidden="true">
								→
							</span>
						) : null}
						<ChainStep {...step} />
					</Fragment>
				))}
			</div>
		</div>
	);
}

/**
 * The rest of the row's forensics, in the order they get asked about: what the
 * upstream called this (ids), what was sent (effort, session), and what it cost
 * (token split, cache, speed, price, the credential that served it). Nothing
 * already visible in the collapsed row is repeated.
 */
function LogDetails({ log }: { log: ProxyLog }) {
	const { t } = useI18n();
	const items: Array<{ label: string; value: ReactNode }> = [];
	if (log.upstream_request_id) {
		items.push({
			label: t("logsPage.upstreamRequestId"),
			value: <code>{log.upstream_request_id}</code>,
		});
	}
	if (log.reasoning_effort) {
		items.push({
			label: t("logsPage.reasoningEffort"),
			value: (
				<code>
					{log.reasoning_effort}
					{log.mapped_reasoning_effort
						? ` → ${log.mapped_reasoning_effort}`
						: ""}
				</code>
			),
		});
	}
	if (log.session_key) {
		items.push({
			label: t("logsPage.detail.session"),
			value: <code>{log.session_key}</code>,
		});
	}
	// A failed attempt meters nothing; "0 / 0" would read like a model that
	// answered with no tokens, so the split only appears when it means something.
	if (log.prompt_tokens || log.completion_tokens) {
		items.push({
			label: t("logsPage.detail.tokenSplit"),
			value: `${log.prompt_tokens ?? 0} / ${log.completion_tokens ?? 0}`,
		});
	}
	if (log.cache_read_tokens || log.cache_creation_tokens) {
		items.push({
			label: t("common.cacheTokens"),
			value: `${log.cache_read_tokens ?? 0} / ${log.cache_creation_tokens ?? 0}`,
		});
	}
	if (log.stream && log.first_byte_ms) {
		items.push({
			label: t("common.firstByte"),
			value: t("common.ms", { n: log.first_byte_ms }),
		});
	}
	if (log.tokens_per_second) {
		items.push({
			label: t("logsPage.throughput"),
			value: `${log.tokens_per_second.toFixed(1)} tok/s`,
		});
	}
	if (log.cost != null) {
		items.push({ label: t("common.cost"), value: formatCost(log.cost) });
	}
	if (log.key_fingerprint) {
		items.push({
			label: t("logsPage.detail.keyFingerprint"),
			value: (
				<code title={t("logsPage.detail.keyFingerprintHint")}>
					{log.key_fingerprint}
				</code>
			),
		});
	}
	return (
		<div className="log-details">
			<strong className="log-details-head">{t("logsPage.detailTitle")}</strong>
			<dl className="log-details-grid">
				{items.map((item) => (
					<Fragment key={item.label}>
						<dt>{item.label}</dt>
						<dd>{item.value}</dd>
					</Fragment>
				))}
			</dl>
		</div>
	);
}

export function LogsView({
  source,
  caps,
}: {
  source: LogsSource;
  caps: LogsCapabilities;
}) {
  const { t } = useI18n();
  const [params, setParams] = useSearchParams();
	const channelId = caps.upstream ? positiveId(params.get("channel_id")) : undefined;
	const modelParam = params.get("model")?.trim() || "";
	const failedOnly = params.get("status") === "failed";
	const upstreamIdParam = caps.upstream ? params.get("upstream_request_id")?.trim() || "" : "";
	const keyIdParam = positiveId(params.get("downstream_key_id"));
	const [modelDraft, setModelDraft] = useState(modelParam);
	const [upstreamIdDraft, setUpstreamIdDraft] = useState(upstreamIdParam);
	const queryParam = params.get("q")?.trim() || "";
	const [queryDraft, setQueryDraft] = useState(queryParam);
	const [slowOnly, setSlowOnly] = useState(false);
  const [showExactFilters, setShowExactFilters] = useState(Boolean(modelParam || upstreamIdParam || keyIdParam));
	const [expandedRequest, setExpandedRequest] = useState<string | null>(null);
	useEffect(() => setModelDraft(modelParam), [modelParam]);
	useEffect(() => setUpstreamIdDraft(upstreamIdParam), [upstreamIdParam]);
	useEffect(() => setQueryDraft(queryParam), [queryParam]);
	const hasFilters = Boolean(channelId || keyIdParam || modelParam || upstreamIdParam || queryParam || modelDraft || upstreamIdDraft || queryDraft || failedOnly || slowOnly);
  const range = useUrlTimeRange(params, setParams);
  const sample = histogramSample(Boolean(range.since || range.until));

	const filters = useMemo(
		() => ({
			channel_id: channelId,
			downstream_key_id: keyIdParam,
			model: modelParam || undefined,
			status: failedOnly ? ("failed" as const) : undefined,
			upstream_request_id: upstreamIdParam || undefined,
			q: queryParam || undefined,
			since: range.since,
			until: range.until,
			limit: 100,
		}),
		[channelId, keyIdParam, failedOnly, modelParam, upstreamIdParam, queryParam, range.since, range.until],
	);

  const logs = useQuery({
    queryKey: ["proxy-logs", filters, caps.upstream ? "admin" : "member"],
    queryFn: ({ signal }) => source.logs(filters, signal),
  });
  // The distribution follows the same window as the list, so "what happened
  // just now" and "what is slow" can never disagree about the time span.
  const histogram = useQuery({
    queryKey: [
      "proxy-log-histogram",
      { since: range.since, until: range.until, sample, scope: caps.upstream ? "admin" : "member" },
    ],
    // Sources provide the same histogram contract. A source without it
    // retains time filters, but must not fabricate a distribution.
    enabled: Boolean(source.latencyHistogram),
    queryFn: ({ signal }) =>
      source.latencyHistogram!(sample, signal, {
        since: range.since,
        until: range.until,
      }),
  });
  const channels = useQuery({
    queryKey: ["channels", caps.upstream ? "admin" : "member"],
    queryFn: ({ signal }) =>
      source.channels ? source.channels(signal) : Promise.resolve([]),
  });
  const channelName = useMemo(() => {
    const map = new Map<number, string>();
    for (const channel of channels.data ?? []) {
      map.set(channel.id, channel.name);
    }
    return map;
  }, [channels.data]);
  // Client tokens: a log row stores the id, so both the filter label and the
  // expanded "which token was this" line need the name from the token list.
  const keys = useQuery({
    queryKey: ["keys", caps.upstream ? "admin" : "member"],
    queryFn: ({ signal }) => source.keys(signal),
  });
  const keyName = useMemo(() => {
    const map = new Map<number, string>();
    for (const key of keys.data ?? []) {
      map.set(key.id, key.name);
    }
    return map;
  }, [keys.data]);

  const rows = logs.data ?? [];
  const visibleRows = slowOnly ? rows.filter((log) => log.latency_ms >= 5000) : rows;
  const pagination = useClientPagination(
    visibleRows,
    20,
  );
  const pageRows = pagination.pageItems;
  const failedCount = visibleRows.filter((log) => log.status >= 400).length;

  // Rows are newest-first; a row was retried when a newer row of the same
  // request carries a higher attempt number.
  const retriedRowIds = new Set<number>();
  const maxAttemptByRequest = new Map<string, number>();
  for (const log of rows) {
    const seen = maxAttemptByRequest.get(log.request_id);
    if (seen !== undefined && seen > log.attempt) retriedRowIds.add(log.id);
    if ((seen ?? 0) < log.attempt) {
      maxAttemptByRequest.set(log.request_id, log.attempt);
    }
  }

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

	// One definition for both entry points (the Clear button and the
	// filtered-empty state), so the two can never drift apart.
	const clearFilters = () => {
		setModelDraft("");
		setUpstreamIdDraft("");
		setQueryDraft("");
		setSlowOnly(false);
		setFilter({
			channel_id: null,
			downstream_key_id: null,
			model: null,
			status: null,
			upstream_request_id: null,
			q: null,
		});
	};

  const histogramData = histogram.data;
  const sampled = histogramData?.total ?? 0;
  const slowShare = sampled > 0 ? (histogramData?.slow_count ?? 0) / sampled : 0;
  const maxBucket = Math.max(1, ...(histogramData?.buckets ?? [1]));
  // Older gateways omit matched/p99; treat them as "the sample is the window".
  const matched = histogramData?.matched ?? sampled;
  const p50 = histogramData?.p50_ms ?? 0;
  const p95 = histogramData?.p95_ms ?? 0;
  const p99 = histogramData?.p99_ms ?? 0;

  /**
   * Export exactly what the current filters matched (not just the visible
   * page) — the usual reason to open this page is to hand the evidence to
   * someone else.
   */
  const exportCSV = () => {
    const header = [
      t("common.time"),
      t("common.model"),
      ...(caps.upstream ? [t("common.route"), t("common.channel")] : []),
      t("common.status"),
      t("logsPage.reasoningEffort"),
      t("common.tokens"),
      t("common.cacheTokens"),
      t("common.latency"),
      t("common.firstByte"),
      t("common.cost"),
      t("common.clientFamily"),
      ...(caps.upstream ? [t("logsPage.upstreamRequestId")] : []),
      "request_id",
      ...(caps.upstream ? [t("logsPage.errorDetail")] : []),
    ];
    const body = visibleRows.map((log) => [
      log.created_at,
      log.model,
      ...(caps.upstream ? [log.route_pattern || "", channelName.get(log.channel_id) ?? `#${log.channel_id}`] : []),
      log.status,
      log.reasoning_effort ?? "",
      log.total_tokens ?? 0,
      `${log.cache_read_tokens ?? 0}/${log.cache_creation_tokens ?? 0}`,
      log.latency_ms,
      log.first_byte_ms ?? "",
      log.cost ?? "",
      log.client_family ?? "",
      ...(caps.upstream ? [log.upstream_request_id ?? ""] : []),
      log.request_id,
      ...(caps.upstream ? [log.error_detail ?? log.error_brief ?? ""] : []),
    ]);
    downloadText(
      timestampedName("meta-gateway-logs", "csv"),
      toCSV([header, ...body]),
    );
  };

  return (
    <>
      {/* 读数条先行：这一页现在有多少条、错了几条，是打开页面第一个问题。 */}
      <div className="logs-overview">
        <TelemetryStrip
          items={[
            {
              label: t("logsPage.stat.shown"),
              value: logs.isPending ? "—" : visibleRows.length,
              tone: "primary",
            },
            {
              label: t("logsPage.stat.failed"),
              value: logs.isPending ? "—" : failedCount,
              tone: "danger",
            },
            {
              label: t("logsPage.stat.failRate"),
              value:
                logs.isPending || visibleRows.length === 0
                  ? "—"
                  : `${Math.round((failedCount / visibleRows.length) * 100)}%`,
              tone: visibleRows.length === 0 || failedCount / Math.max(1, visibleRows.length) < 0.05 ? "success" : "warning",
            },
          ]}
        />
        <div className="logs-overview-actions">
          <Button
            variant="secondary"
            icon={<Download size={15} />}
            disabled={visibleRows.length === 0}
            onClick={exportCSV}
          >
            {t("logsPage.export")}
          </Button>
          <Button
            variant="secondary"
            icon={<RefreshCw size={16} />}
            onClick={() => {
              void logs.refetch();
              if (source.latencyHistogram) void histogram.refetch();
            }}
          >
            {t("common.refresh")}
          </Button>
        </div>
      </div>

      {/* 延迟分布 — 一等公民面板，紧跟读数条（原为页面底部的折叠条）。 */}
      <Panel className="latency-panel">
        <div className="panel-header latency-panel-header">
          <div className="cockpit-panel-title">
            <Timer size={14} />
            <strong>{t(source.latencyHistogram ? "logsPage.histogram" : "logsPage.timeWindow")}</strong>
          </div>
          <span className="panel-muted latency-panel-scope">
            {/* A bounded window is already spelled out by the picker's own
                caption; only the unbounded case needs a sample note. */}
            {!source.latencyHistogram || range.since || range.until
              ? null
              : t(caps.upstream ? "logsPage.histogramScope" : "logsPage.memberHistogramScope", { n: sample.toLocaleString() })}
          </span>
          <TimeRangePicker range={range} compact onRefresh={() => { void logs.refetch(); if (source.latencyHistogram) void histogram.refetch(); }} />
        </div>
        {!source.latencyHistogram ? null : histogram.isPending ? (
          <p className="dashboard-empty">{t("common.working")}</p>
        ) : histogram.isError ? (
          <ErrorState error={histogram.error} retry={() => void histogram.refetch()} />
        ) : !histogramData || histogramData.total === 0 ? (
          <p className="dashboard-empty">{t("logsPage.histogramEmpty")}</p>
        ) : (
          <>
            <div
              className="latency-histogram-bars"
              role="img"
              aria-label={t("logsPage.histogramStats", {
                total: histogramData.total,
                slow: histogramData.slow_count,
                p50,
                p95,
                p99,
              })}
            >
              {histogramData.buckets.map((count, index) => (
                <div
                  key={index}
                  className={`latency-histogram-bar${
                    index >= HISTOGRAM_SLOW_FROM ? " is-slow" : ""
                  }`}
                  style={{ height: `${Math.max(4, (count / maxBucket) * 100)}%` }}
                  title={t("logsPage.histogramBucket", {
                    n: count,
                    share: `${((count / Math.max(1, histogramData.total)) * 100).toFixed(1)}%`,
                  })}
                />
              ))}
            </div>
            <div className="latency-histogram-labels">
              {HISTOGRAM_LABELS.map((label, index) => (
                <span key={index}>{label}</span>
              ))}
            </div>
            <div className="latency-readouts">
              <div className="latency-readout">
                <span>{t("logsPage.stat.total")}</span>
                <strong>{histogramData.total.toLocaleString()}</strong>
              </div>
              <div className="latency-readout">
                <span>{t("logsPage.stat.slow")}</span>
                <strong>{histogramData.slow_count.toLocaleString()}</strong>
              </div>
              <div className={`latency-readout${slowShare >= 0.05 ? " is-warn" : ""}`}>
                <span>{t("logsPage.histogramSlowShare")}</span>
                <strong>{(slowShare * 100).toFixed(1)}%</strong>
              </div>
              <div className="latency-readout">
                <span>p50</span>
                <strong>{p50} ms</strong>
              </div>
              <div className="latency-readout">
                <span>p95</span>
                <strong>{p95} ms</strong>
              </div>
              <div className="latency-readout">
                <span>p99</span>
                <strong>{p99} ms</strong>
              </div>
              <div className="latency-readout is-coverage">
                <span>{t("logsPage.coverage")}</span>
                <strong>
                  {matched <= sampled
                    ? t("logsPage.histogramCovered", {
                        matched: matched.toLocaleString(),
                      })
                    : t("logsPage.histogramCoverage", {
                        matched: matched.toLocaleString(),
                        sample: sampled.toLocaleString(),
                      })}
                </strong>
              </div>
            </div>
            <p className="latency-panel-hint">{t("logsPage.rangeScope")}</p>
          </>
        )}
      </Panel>

      <div className="logs-split">
        <Panel className="ops-list-panel">
          <form className="filter-bar log-filter-bar" onSubmit={(event) => {
            event.preventDefault();
            setFilter({ model: modelDraft.trim() || null, q: queryDraft.trim() || null, upstream_request_id: upstreamIdDraft.trim() || null });
          }}>
            {caps.upstream ? (
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
            ) : null}
			<input
				className="log-search-input"
				aria-label={t("logsPage.search")}
				value={queryDraft}
				placeholder={t("logsPage.search")}
				onChange={(e) => setQueryDraft(e.target.value)}
			/>
            <Button variant="secondary" icon={<SlidersHorizontal size={14} />} aria-expanded={showExactFilters} aria-controls="proxy-log-exact-filters" onClick={() => setShowExactFilters((value) => !value)}>
              {t("logsPage.moreFilters")}
            </Button>
            <Button
              variant="secondary"
              type="submit"
            >
              {t("common.apply")}
            </Button>
			{hasFilters && (
				<Button
					variant="quiet"
					onClick={clearFilters}
			  >
                {t("common.clearFilters")}
              </Button>
            )}
            <div className="log-exact-filters" id="proxy-log-exact-filters" hidden={!showExactFilters}>
			<input
				value={modelDraft}
				aria-label={t("common.model")}
				placeholder={t("common.model")}
				onChange={(e) => setModelDraft(e.target.value)}
			/>
            {caps.upstream ? (
			<input
				value={upstreamIdDraft}
				aria-label={t("logsPage.upstreamRequestId")}
				placeholder={t("logsPage.upstreamRequestId")}
				onChange={(e) => setUpstreamIdDraft(e.target.value)}
			/>
            ) : null}
			<select
				aria-label={t("logsPage.token")}
				value={keyIdParam ?? 0}
				onChange={(event) => {
					const value = Number(event.target.value);
					setFilter({ downstream_key_id: value > 0 ? String(value) : null });
				}}
			>
				<option value={0}>{t("logsPage.filterAllTokens")}</option>
				{/* A filter pointing at a token that no longer exists must stay
				    visible: silently dropping it back to "all" would show other
				    tokens' rows under a filter the user still sees in the URL. */}
				{keyIdParam && !keyName.has(keyIdParam) ? (
					<option value={keyIdParam}>
						{t("logsPage.filterMissingToken", { id: keyIdParam })}
					</option>
				) : null}
				{(keys.data ?? []).map((key) => (
					<option key={key.id} value={key.id}>
						{key.name}
					</option>
				))}
			</select>
            </div>
          </form>
          <div className="log-quick-filters">
        <div className="log-filter-switches">
          <label className="check marginless">
            <input
              type="checkbox"
              checked={failedOnly}
              onChange={(e) =>
                setFilter({ status: e.target.checked ? "failed" : null })
              }
            />
            <span>{t("logsPage.failedOnly")}</span>
          </label>
          <label className="check marginless">
            <input
              type="checkbox"
              checked={slowOnly}
              onChange={(e) => setSlowOnly(e.target.checked)}
            />
            <span>{t("logsPage.slowOnly")}</span>
          </label>
        </div>
          </div>

          <EntityState
            isLoading={logs.isPending}
            isError={logs.isError}
            error={logs.error}
            isEmpty={!rows.length}
            empty={
              hasFilters ? (
                // "No proxy traffic yet" would be a lie here: rows exist, they
                // just do not match. Say which one it is, and offer the exit.
                <EmptyHero
                  kicker={t("logsPage.emptyFilteredKicker")}
                  title={t("logsPage.emptyFilteredTitle")}
                  body={t("logsPage.emptyFiltered")}
                  actions={
                    <Button variant="secondary" onClick={clearFilters}>
                      {t("common.clearFilters")}
                    </Button>
                  }
                />
              ) : (
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
              )
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
									// The operator's view of a row: which route and which
									// channel served it, and what it cost. A member sees their
									// own requests — the same table without the site's internals.
									...(caps.upstream ? [t("common.route"), t("common.channel")] : []),
									t("common.status"),
									t("common.tokens"),
									t("common.cacheTokens"),
									t("common.latency"),
									t("common.firstByte"),
									...(caps.pricing ? [t("common.cost")] : []),
									t("common.clientFamily"),
								]}
							>
								{pageRows.map((log) => {
									return (
										<Fragment key={log.id}>
										<tr
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
										<strong className="log-model-name" title={log.model}>{log.model}</strong>
										{log.upstream_model && log.upstream_model !== log.model && caps.upstream ? (
											// Shared aliases: one client-facing name served by
											// several real upstream models. Without this the row
											// cannot be attributed after the fact.
											<span
												className="log-upstream-model"
												title={t("logsPage.upstreamModelHint")}
											>
												{t("routing.memberOrigin", {
													model: log.upstream_model,
												})}
											</span>
										) : null}
										<small className="mono" title={log.request_id}>{log.request_id}</small>
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
									{caps.upstream ? (
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
									) : null}
									{caps.upstream ? (
									<td>
										<Link
											to={`/channels?id=${log.channel_id}`}
											title={t("logsPage.openConnection")}
										>
											{channelName.get(log.channel_id) ??
												`#${log.channel_id}`}
										</Link>
										{/* The endpoint this attempt actually called. It differs from the
										    client path whenever a channel endpoint override/map or a
										    custom-path passthrough relocated the request, and it is the
										    only place that shows a per-model retarget. */}
										{log.upstream_url ? (
											<small
												className="mono log-upstream-url"
												title={t("logsPage.upstreamUrlHint")}
											>
												{log.upstream_url}
											</small>
										) : null}
									</td>
									) : null}
									<td className="log-status-cell">
										<span className="log-status-line">
											<span className={`log-status-light${log.status >= 400 ? " is-bad" : log.status >= 300 ? " is-warn" : " is-ok"}`} aria-hidden="true" />
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
											{retriedRowIds.has(log.id) ? (
												<span
													className="log-retry-mark"
													title={t("logsPage.retried")}
												>
													{t("logsPage.retried")}
												</span>
											) : null}
										</span>
									</td>
									<td>{log.total_tokens ? log.total_tokens : "—"}</td>
									<td>
										{log.cache_read_tokens || log.cache_creation_tokens
											? `${log.cache_read_tokens ?? 0} / ${log.cache_creation_tokens ?? 0}`
											: "—"}
									</td>
									<td className="log-latency-cell">
										{/* A zero-latency attempt (a failure before any wait) drew
										    the empty track as a stray grey line. */}
										{log.latency_ms > 0 ? (
										<span
											className="log-latency-bar"
											aria-hidden="true"
											title={t("logsPage.latencyBarHint", {
												n: log.latency_ms,
												pct: Math.round(latencyShare(log.latency_ms) * 100),
											})}
										>
											<span
												className={log.latency_ms >= 5000 ? "is-slow" : log.latency_ms >= 1000 ? "is-warn" : ""}
												style={{ transform: `scaleX(${latencyShare(log.latency_ms)})` }}
											/>
										</span>
									) : null}
										{t("common.ms", { n: log.latency_ms })}
									</td>
									<td className="log-ttfb-cell">
										{log.stream && log.first_byte_ms
											? t("common.ms", { n: log.first_byte_ms })
											: "—"}
									</td>
									{caps.pricing ? (
									<td className="log-cost">
										{log.cost != null ? formatCost(log.cost) : "—"}
									</td>
									) : null}
									<td>{log.client_family || "—"}</td>
									</tr>
										{expandedRequest === log.request_id ? (
											<tr className="log-decision-row">
											<td colSpan={caps.upstream ? 12 : caps.pricing ? 11 : 10}>
												<div className="log-expand">
													{/* Where the request came from and where it went, before the
													    forensics: "who called, through what" is what an
													    operator asks first about a row they do not recognise. */}
													{caps.upstream ? <LogChain log={log} channelName={channelName} keyName={keyName} /> : null}
													<div className="log-expand-grid">
													{log.error_detail ? (
														<div className="log-error-detail">
															<div className="log-error-detail-head">
																<strong>{t("logsPage.errorDetail")}</strong>
																<code>HTTP {log.status}</code>
															</div>
															<pre>{log.error_detail}</pre>
														</div>
													) : null}
													<LogDetails log={log} />
													{caps.decision ? (
													<DecisionSnapshotView
														requestId={log.request_id}
														attempt={log.attempt}
													/>
													) : null}
													</div>
												</div>
											</td>
										</tr>
										) : null}
										</Fragment>
									);
								})}
							</DataTable>
            </ListShell>
          </EntityState>
        </Panel>
      </div>
    </>
  );
}

export function Logs() {
  const { client, role } = useSession();
  const { t } = useI18n();
  const member = role === "member";
  const service = useMemo(() => api(client!), [client]);
  // One binding per role: staff see every upstream view, a member sees their
  // own requests through the member source. The tab list below is filtered to
  // match, because the discovery and audit panels are the gateway's business.
  const source = useMemo<LogsSource>(
    () =>
      member
        ? memberLogsSource
        : {
            logs: (filters, signal) => service.proxyLogs(filters, signal),
            latencyHistogram: (sample, signal, window) =>
              service.proxyLogLatencyHistogram(sample, signal, window),
            channels: (signal) => service.channels(signal),
            keys: (signal) => service.keys(signal) as never,
            decisionSnapshot: (requestId, attempt, signal) =>
              service.decisionSnapshot(requestId, attempt, signal),
          },
    [member, service],
  );
  const [params, setParams] = useSearchParams();
  const rawTab = params.get("tab");
  const tabItems = [
    { value: "proxy", label: t("logsPage.tab.proxy") },
    ...(member
      ? []
      : [
          { value: "live", label: t("logsPage.tab.live"), icon: <MessagesSquare size={14} /> },
          { value: "discovery", label: t("logsPage.tab.discovery") },
          { value: "audit", label: t("logsPage.tab.audit") },
        ]),
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
      description={t(member ? "logsPage.memberDescription" : "logsPage.hubDescription")}
    >
      <div className="ops-canvas">
        <Tabs items={tabItems} active={active} onChange={changeTab} />
        {active === "proxy" ? (
          <LogsView
            source={source}
            caps={member ? MEMBER_LOG_CAPS : ADMIN_LOG_CAPS}
          />
        ) : null}
        {active === "live" ? <LiveTracePanel /> : null}
        {active === "discovery" ? <DiscoveryPanel /> : null}
        {active === "audit" ? <AuditPanel /> : null}
      </div>
    </Page>
  );
}
