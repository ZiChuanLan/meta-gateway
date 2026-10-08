import { ChevronRight, Search } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { api } from "../../api/client";
import type {
  Site,
  SiteProbeAction,
  SiteProbeDetection,
  SiteProbeNameOnly,
  SiteProbePolicy,
  SiteProbePrice,
  SiteProbeRow,
  SiteProbeSiteStatus,
} from "../../api/types";
import { Button, Dialog, Empty, ErrorState, InfoTip } from "../../components/ui";
import { useAdminMutation } from "../../hooks/useAdminMutation";
import { useI18n } from "../../i18n";
import { useSession } from "../../session";

const DEFAULT_POLICY: SiteProbePolicy = {
  ratio_threshold: 0.9,
  min_samples: 5,
  low_rounds: 2,
  high_rounds: 2,
};

/**
 * A cadence in seconds, in the largest whole unit that stays exact: the setting
 * is stored in seconds (minimum 60), so "900" has to read as "15 分钟" or the
 * operator cannot compare it with the last-run timestamps next to it.
 */
function formatCadence(seconds: number, t: Translate) {
  if (seconds <= 0) return t("modelsPage.siteProbe.unitSeconds", { count: 0 });
  if (seconds % 3600 === 0) return t("modelsPage.siteProbe.unitHours", { count: seconds / 3600 });
  if (seconds % 60 === 0) return t("modelsPage.siteProbe.unitMinutes", { count: seconds / 60 });
  return t("modelsPage.siteProbe.unitSeconds", { count: seconds });
}

/** How many unmatched models are listed before the operator asks for more. */
const UNMATCHED_PAGE = 20;

/** The default monitoring directory. Importing copies addresses, never data. */
export const CATALOG_URL = "https://watchbot.cfd/api/v1/public/dashboard";

/**
 * Site probe: what the sites themselves publish about their models, plus what
 * our own relay already knows.
 *
 * Two rules shape this screen:
 *
 *  1. Nothing here costs upstream tokens. The data comes from pages a site
 *     publishes publicly (a New-API price table, an Uptime Kuma status page),
 *     and where a site publishes no availability at all we fall back to our own
 *     relay logs — real requests we already served, not synthetic probes.
 *  2. Collecting is free and automatic; changing routing is not. The operator
 *     previews the impact and then applies it, and a site can opt into applying
 *     its own verdicts unattended (card switch) without affecting the others.
 *
 * The screen is one card per site, because that is the unit an operator thinks
 * in ("is this site any good?"), and the unit the switches act on.
 */
export function SiteProbeDialog({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const { client } = useSession();
  const service = api(client!);
  const [policy, setPolicy] = useState<SiteProbePolicy>(DEFAULT_POLICY);
  const [sourcePolicy, setSourcePolicy] = useState<SiteProbePolicy>(DEFAULT_POLICY);
  const [scopeSite, setScopeSite] = useState("");
  const previewKey = useRef("");
  const [previewFor, setPreviewFor] = useState("");
  const [url, setUrl] = useState("");
  const [siteId, setSiteId] = useState<string>("");
  const [detection, setDetection] = useState<SiteProbeDetection | null>(null);
  const [actions, setActions] = useState<SiteProbeAction[] | null>(null);
  const [actionsAreDryRun, setActionsAreDryRun] = useState(true);
  const [query, setQuery] = useState("");
  const [onlyWithData, setOnlyWithData] = useState(false);
  // The rail's own filter and selection: separate from `scopeSite`, which is the
  // ACTION scope (what a preview/apply touches) and must not move when the
  // operator is merely looking at a different site.
  const [railFilter, setRailFilter] = useState<"all" | "attached" | "candidates" | "issues">("all");
  const [activeSiteId, setActiveSiteId] = useState<number | null>(null);
  const [unmatchedQuery, setUnmatchedQuery] = useState("");
  const [showAllUnmatched, setShowAllUnmatched] = useState(false);
  const [pruneArmed, setPruneArmed] = useState(false);

  const sites = useQuery({
    queryKey: ["sites"],
    queryFn: ({ signal }) => service.sites(signal),
  });
  const report = useQuery({
    queryKey: ["site-probe", policy],
    queryFn: ({ signal }) => service.siteProbeReport(policy, signal),
  });
  // The collection cadence is a runtime setting, not a property of this dialog.
  // It is read here only so the operator can see how fresh these numbers can be
  // — and where to change it — without leaving the readings they are judging.
  const runtime = useQuery({
    queryKey: ["runtime-settings"],
    queryFn: ({ signal }) => service.runtimeSettings(signal),
  });

  const detect = useAdminMutation({
    mutationFn: (target: { url: string; siteId: string }) => service.siteProbeDetect(target.url),
    onSuccess: (data, target) => {
      if (
        target.url === sourceDraft.current.url.trim() &&
        target.siteId === sourceDraft.current.siteId
      )
        setDetection(data);
    },
  });
  const save = useAdminMutation({
    mutationFn: (body: {
      site_id: number;
      kind: string;
      url: string;
      auto: boolean;
      enabled: boolean;
      config: string;
    }) => service.saveSiteProbeSource(body),
    invalidateKeys: [["sites"], ["site-probe"]],
    onSuccess: (_data, body) => collect.mutate([body.site_id]),
  });
  const clear = useAdminMutation({
    mutationFn: (id: number) => service.clearSiteProbeSource(id),
    invalidateKeys: [["sites"], ["site-probe"]],
    onSuccess: () => setDetection(null),
  });
  const collect = useAdminMutation({
    mutationFn: (ids?: number[]) => service.siteProbeCollect(ids),
    invalidateKeys: [["site-probe"], ["sites"]],
  });
  // One click: the backend fetches the directory, matches it onto the sites we
  // already have and reads every matched site once. No preview step — the
  // readings themselves are the confirmation.
  const catalogImport = useAdminMutation({
    mutationFn: (body: { url: string }) => service.siteProbeCatalogImport(body),
    invalidateKeys: [["sites"], ["site-probe"]],
  });
  // Cleanup for the debris an earlier import left behind: sites nothing routes
  // through. The backend refuses any site that has a channel or a credential.
  const catalogPrune = useAdminMutation({
    mutationFn: () => service.siteProbeCatalogPrune(),
    invalidateKeys: [["sites"], ["site-probe"]],
  });
  // Per-card switches: the source on/off and the unattended apply mode are the
  // only per-site acts the common flow needs, so they live on the card itself.
  const toggleSource = useAdminMutation({
    mutationFn: (site: SiteProbeSiteStatus) =>
      service.saveSiteProbeSource({
        site_id: site.site_id,
        kind: site.probe_source_kind ?? "",
        url: site.probe_source_url ?? "",
        auto: site.probe_auto ?? true,
        enabled: !site.probe_source_enabled,
        config: site.probe_source_config ?? "{}",
      }),
    invalidateKeys: [["sites"], ["site-probe"]],
    pendingIdOf: (site: SiteProbeSiteStatus) => site.site_id,
  });
  const toggleAutoApply = useAdminMutation({
    mutationFn: (site: SiteProbeSiteStatus) =>
      service.saveSiteProbeSource({
        site_id: site.site_id,
        kind: site.probe_source_kind ?? "",
        url: site.probe_source_url ?? "",
        auto: site.probe_auto ?? true,
        enabled: true,
        // The site's own policy is echoed back with the switch, so flipping it
        // never silently resets thresholds someone tuned for that site.
        config: JSON.stringify({
          auto_apply: !site.auto_apply,
          policy: site.policy ?? policy,
        }),
      }),
    invalidateKeys: [["sites"], ["site-probe"]],
    pendingIdOf: (site: SiteProbeSiteStatus) => -site.site_id,
  });
  const collectOne = useAdminMutation({
    mutationFn: (id: number) => service.siteProbeCollect([id]),
    invalidateKeys: [["site-probe"], ["sites"]],
    pendingIdOf: (id: number) => id,
  });
  const apply = useAdminMutation({
    mutationFn: (body: {
      policy: SiteProbePolicy;
      dry_run: boolean;
      site_ids?: number[];
      previewKey: string;
    }) =>
      service.siteProbeApply({
        policy: body.policy,
        dry_run: body.dry_run,
        site_ids: body.site_ids,
      }),
    invalidateKeys: [["route-overviews"], ["channels"]],
    onSuccess: (data, variables) => {
      if (variables.previewKey !== previewKey.current) return;
      setPreviewFor(variables.previewKey);
      setActions(data.actions);
      setActionsAreDryRun(variables.dry_run);
      if (!variables.dry_run) void report.refetch();
    },
  });
  const adopt = useAdminMutation({
    mutationFn: (memberIds: number[]) => service.siteProbeAdoptPrice(memberIds),
    invalidateKeys: [["site-probe"], ["route-overviews"], ["channels"], ["model-pricing"]],
  });

  const validPolicy = (value: SiteProbePolicy) =>
    value.ratio_threshold > 0 &&
    value.ratio_threshold <= 1 &&
    Number.isInteger(value.min_samples) &&
    value.min_samples >= 1 &&
    value.min_samples <= 1000 &&
    Number.isInteger(value.low_rounds) &&
    value.low_rounds >= 1 &&
    value.low_rounds <= 10 &&
    Number.isInteger(value.high_rounds) &&
    value.high_rounds >= 1 &&
    value.high_rounds <= 10;

  const policyFromDraft = (patch: Partial<SiteProbePolicy>) => {
    setPolicy({ ...policy, ...patch });
    // A threshold change invalidates the preview: the listed actions were
    // computed under the old policy, and applying them would use the new one.
    setActions(null);
  };

  // Stable references: `report.data` changes on every refetch, so derive the
  // arrays once instead of rebuilding them in each render.
  const rows = useMemo(() => report.data?.rows ?? [], [report.data]);
  const siteRows = useMemo(() => report.data?.sites ?? [], [report.data]);
  const unmatchedRows = useMemo(() => report.data?.unmatched ?? [], [report.data]);
  // Name-only matches: a site publishes a model whose name matches one of our
  // routes, but the route has no member there. Shown as candidates, never as
  // readings — there is nothing on this site to judge, disable or price.
  const nameOnlyRows = useMemo(() => report.data?.name_only ?? [], [report.data]);
  const needle = query.trim().toLowerCase();
  const visibleRows = useMemo(() => {
    return rows.filter((row) => {
      if (scopeSite && row.site_id !== Number(scopeSite)) return false;
      if (onlyWithData && !rowHasEvidence(row)) return false;
      if (needle === "") return true;
      return `${row.route} ${row.raw_model} ${row.site_name}`.toLowerCase().includes(needle);
    });
  }, [rows, needle, onlyWithData, scopeSite]);
  const rowsBySite = useMemo(() => {
    const grouped = new Map<number, SiteProbeRow[]>();
    for (const row of visibleRows) {
      const bucket = grouped.get(row.site_id) ?? [];
      bucket.push(row);
      grouped.set(row.site_id, bucket);
    }
    return grouped;
  }, [visibleRows]);
  const candidatesBySite = useMemo(() => {
    const grouped = new Map<number, SiteProbeNameOnly[]>();
    for (const candidate of nameOnlyRows) {
      if (
        needle !== "" &&
        !`${candidate.raw_model} ${candidate.route}`.toLowerCase().includes(needle)
      )
        continue;
      const bucket = grouped.get(candidate.site_id) ?? [];
      bucket.push(candidate);
      grouped.set(candidate.site_id, bucket);
    }
    return grouped;
  }, [nameOnlyRows, needle]);
  const railFiltered = useMemo(() => {
    const list = siteRows
      .filter((site) => !scopeSite || site.site_id === Number(scopeSite))
      .sort((left, right) => left.site_name.localeCompare(right.site_name));
    if (railFilter === "all") return list;
    return list.filter((site) => {
      const attached = rowsBySite.get(site.site_id) ?? [];
      const candidates = candidatesBySite.get(site.site_id) ?? [];
      if (railFilter === "attached") return attached.length > 0;
      if (railFilter === "candidates") return candidates.length > 0;
      return (
        !!site.probe_last_error ||
        attached.some(
          (row) =>
            row.verdict === "low" || (row.members ?? []).some((member) => member.auto_disabled),
        )
      );
    });
  }, [siteRows, rowsBySite, candidatesBySite, railFilter, scopeSite]);
  // The rail is the site list; the detail pane follows the selection. A search
  // narrows the list without stealing the selection, so typing never blanks the
  // pane the operator is reading.
  const activeSite = useMemo(() => {
    const inScope = railFiltered.length > 0 ? railFiltered : siteRows;
    return inScope.find((site) => site.site_id === activeSiteId) ?? inScope[0];
  }, [railFiltered, siteRows, activeSiteId]);
  // Cards follow the report's site list (so a configured site with no reading
  // yet still gets its card and its switch), ordered by name for a stable page.
  const cards = useMemo(() => {
    const list = siteRows
      .filter((site) => !scopeSite || site.site_id === Number(scopeSite))
      .sort((left, right) => left.site_name.localeCompare(right.site_name));
    if (needle === "" && !onlyWithData) return list;
    return list.filter(
      (site) =>
        (rowsBySite.get(site.site_id)?.length ?? 0) > 0 ||
        (!onlyWithData && needle !== "" && site.site_name.toLowerCase().includes(needle)),
    );
  }, [siteRows, rowsBySite, needle, onlyWithData, scopeSite]);

  const unmatchedFiltered = useMemo(() => {
    const term = unmatchedQuery.trim().toLowerCase();
    if (term === "") return unmatchedRows;
    return unmatchedRows.filter((row) =>
      `${row.raw_model} ${row.site_name}`.toLowerCase().includes(term),
    );
  }, [unmatchedRows, unmatchedQuery]);
  const unmatchedShown = showAllUnmatched
    ? unmatchedFiltered
    : unmatchedFiltered.slice(0, UNMATCHED_PAGE);

  const lowCount = visibleRows.filter((row) => row.verdict === "low").length;
  const autoDisabled = visibleRows.reduce(
    (total, row) => total + (row.members ?? []).filter((member) => member.auto_disabled).length,
    0,
  );
  const selectedSite = (sites.data ?? []).find((site) => String(site.id) === siteId);
  // `editable` is guarded too: an older gateway, or a partial payload, must not
  // blank the dialog over a line that is only informative.
  const cadenceSeconds = runtime.data?.editable?.site_probe_interval_seconds ?? 0;
  const cadence =
    cadenceSeconds > 0
      ? {
          interval: formatCadence(cadenceSeconds, t),
          jitter: formatCadence(runtime.data?.editable?.site_probe_jitter_seconds ?? 0, t),
        }
      : null;
  // A site already configured can be edited (thresholds, a manual URL) without
  // re-detecting its page.
  const sourceKind =
    detection?.kind ??
    (url.trim() === selectedSite?.probe_source_url ? selectedSite.probe_source_kind : "") ??
    "";
  const sourceURL = detection?.url ?? url.trim();
  // Auto mode needs no URL at all: the platform column derives the source.
  // It is the primary control; the URL field is the advanced override.
  const [autoMode, setAutoMode] = useState<boolean>(selectedSite?.probe_auto ?? true);
  const sourceDraft = useRef({ siteId, url });
  sourceDraft.current = { siteId, url };
  const canSave =
    siteId !== "" &&
    validPolicy(sourcePolicy) &&
    (autoMode || (sourceKind !== "" && sourceURL !== ""));
  const busy =
    save.isPending ||
    clear.isPending ||
    collect.isPending ||
    collectOne.isPending ||
    catalogImport.isPending ||
    catalogPrune.isPending ||
    toggleSource.isPending ||
    toggleAutoApply.isPending ||
    apply.isPending ||
    adopt.isPending;
  const evaluationKey = JSON.stringify([policy, scopeSite, report.dataUpdatedAt]);
  previewKey.current = evaluationKey;
  useEffect(() => {
    setActions(null);
    setPreviewFor("");
  }, [policy, scopeSite]);
  const submitApply = (dry_run: boolean) =>
    apply.mutate({
      policy,
      dry_run,
      site_ids: scopeSite ? [Number(scopeSite)] : undefined,
      previewKey: evaluationKey,
    });
  // The auto-apply switch is the only writer of that flag (it saves on click), so
  // a thresholds save echoes the site's CURRENT value: sending a draft copy would
  // silently revert a switch flipped after the site was picked.
  const saveDraft = () => {
    const target = siteRows.find((candidate) => String(candidate.site_id) === siteId);
    save.mutate({
      site_id: Number(siteId),
      kind: sourceKind,
      url: sourceURL,
      auto: autoMode,
      enabled: true,
      config: JSON.stringify({ auto_apply: target?.auto_apply ?? false, policy: sourcePolicy }),
    });
  };

  const pickSite = (value: string) => {
    setSiteId(value);
    setDetection(null);
    const site = (sites.data ?? []).find((candidate) => String(candidate.id) === value);
    setUrl(site?.probe_source_url ?? "");
    setAutoMode(site?.probe_auto ?? true);
    const status = siteRows.find((candidate) => String(candidate.site_id) === value);
    setSourcePolicy(status?.policy ?? DEFAULT_POLICY);
  };

  return (
    <Dialog
      title={t("modelsPage.siteProbe.title")}
      onClose={onClose}
      busy={busy}
      actions={
        <>
          <Button variant="secondary" onClick={onClose} disabled={busy}>
            {t("common.close")}
          </Button>
          <Button
            disabled={busy || report.isFetching || report.isError || !validPolicy(policy)}
            onClick={() => submitApply(true)}
          >
            {t("modelsPage.siteProbe.preview")}
          </Button>
          <Button
            disabled={
              busy ||
              report.isFetching ||
              report.isError ||
              actions === null ||
              !actions.some((action) => !action.skipped) ||
              actionsAreDryRun === false ||
              previewFor !== evaluationKey
            }
            onClick={() => submitApply(false)}
          >
            {t("modelsPage.siteProbe.apply")}
          </Button>
        </>
      }
    >
      <p className="unify-intro">{t("modelsPage.siteProbe.description")}</p>

      <div className="site-probe-workflow">{t("modelsPage.siteProbe.workflow")}</div>
      {cadence ? (
        <p className="site-probe-cadence" role="status">
          {t("modelsPage.siteProbe.cadence", {
            interval: cadence.interval,
            jitter: cadence.jitter,
          })}
          <span className="field-hint">· {t("modelsPage.siteProbe.cadenceHint")}</span>
        </p>
      ) : null}
      <label className="field">
        <span>{t("modelsPage.siteProbe.actionScope")}</span>
        <select
          aria-label={t("modelsPage.siteProbe.actionScope")}
          value={scopeSite}
          disabled={busy}
          onChange={(event) => setScopeSite(event.target.value)}
        >
          <option value="">{t("modelsPage.siteProbe.allSitesScope")}</option>
          {siteRows.map((site) => (
            <option key={site.site_id} value={site.site_id}>
              {site.site_name}
            </option>
          ))}
        </select>
        <span className="field-hint">{t("modelsPage.siteProbe.scopeHint")}</span>
      </label>
      <div className="site-probe-bar">
        <button
          type="button"
          className="site-probe-primary"
          disabled={busy}
          onClick={() => catalogImport.mutate({ url: CATALOG_URL })}
        >
          {catalogImport.isPending
            ? t("modelsPage.siteProbe.catalogImporting")
            : t("modelsPage.siteProbe.catalogImport")}
        </button>
        <div className="probe-search">
          <Search size={13} />
          <input
            type="search"
            value={query}
            placeholder={t("modelsPage.siteProbe.search")}
            onChange={(event) => setQuery(event.target.value)}
          />
        </div>
        <label className="check marginless">
          <input
            type="checkbox"
            checked={onlyWithData}
            onChange={(event) => setOnlyWithData(event.target.checked)}
          />
          <span>{t("modelsPage.siteProbe.onlyWithData")}</span>
        </label>
        <span className="site-probe-summary" role="status">
          {t("modelsPage.siteProbe.scope", {
            rows: visibleRows.length,
            low: lowCount,
            ok: visibleRows.filter((row) => row.verdict === "ok").length,
            disabled: autoDisabled,
          })}
        </span>
      </div>

      {catalogImport.data ? (
        <div className="unify-result" role="status">
          {t("modelsPage.siteProbe.catalogDone", {
            matched: catalogImport.data.matched,
            skipped: catalogImport.data.skipped,
            unmatched: catalogImport.data.unmatched,
            collected: catalogImport.data.collected ?? 0,
            failed: catalogImport.data.failed ?? 0,
          })}
        </div>
      ) : null}
      {catalogImport.error ? (
        <div className="inline-error">{String(catalogImport.error)}</div>
      ) : null}

      <h3>{t("modelsPage.siteProbe.previewPolicy")}</h3>
      <div className="site-probe-policy">
        <label className="field">
          <span>{t("modelsPage.siteProbe.threshold")}</span>
          <input
            type="number"
            disabled={busy}
            min={1}
            max={100}
            value={Math.round(policy.ratio_threshold * 100)}
            onChange={(event) =>
              policyFromDraft({
                ratio_threshold: Number(event.target.value) / 100,
              })
            }
          />
        </label>
        <label className="field">
          <span>{t("modelsPage.siteProbe.minSamples")}</span>
          <input
            type="number"
            disabled={busy}
            min={1}
            max={1000}
            value={policy.min_samples}
            onChange={(event) => policyFromDraft({ min_samples: Number(event.target.value) })}
          />
        </label>
        <label className="field">
          <span>{t("modelsPage.siteProbe.lowRounds")}</span>
          <input
            type="number"
            disabled={busy}
            min={1}
            max={10}
            value={policy.low_rounds}
            onChange={(event) => policyFromDraft({ low_rounds: Number(event.target.value) })}
          />
        </label>
        <label className="field">
          <span>{t("modelsPage.siteProbe.highRounds")}</span>
          <input
            type="number"
            disabled={busy}
            min={1}
            max={10}
            value={policy.high_rounds}
            onChange={(event) => policyFromDraft({ high_rounds: Number(event.target.value) })}
          />
        </label>
        <p className="site-probe-policy-hint">
          {t("modelsPage.siteProbe.policyHint", {
            low: policy.low_rounds,
            threshold: Math.round(policy.ratio_threshold * 100),
            samples: policy.min_samples,
          })}
        </p>
      </div>

      {report.isPending ? (
        <Empty>{t("common.loading")}</Empty>
      ) : report.isError ? (
        <ErrorState error={report.error} retry={() => void report.refetch()} />
      ) : cards.length === 0 ? (
        <Empty>{t("modelsPage.siteProbe.noReadings")}</Empty>
      ) : (
        /* A rail instead of a wall of cards: one row per site, always visible, so
           an operator can scan 40 sites and pick the one with a problem. The
           cards put every site side by side, which made the page unusable at
           exactly the fleet size this tool exists for. */
        <div className="site-probe-layout">
          <nav className="site-probe-rail" aria-label={t("modelsPage.siteProbe.railTitle")}>
            <div className="site-probe-rail-head">
              <span className="site-probe-rail-title">{t("modelsPage.siteProbe.railTitle")}</span>
              <div className="site-probe-rail-filters" role="group">
                {(["all", "attached", "candidates", "issues"] as const).map((key) => (
                  <button
                    key={key}
                    type="button"
                    className={`site-probe-rail-filter${railFilter === key ? " is-active" : ""}`}
                    aria-pressed={railFilter === key}
                    onClick={() => setRailFilter(key)}
                  >
                    {t(
                      `modelsPage.siteProbe.rail${key === "all" ? "All" : key === "attached" ? "Attached" : key === "candidates" ? "Candidates" : "Issues"}`,
                    )}
                  </button>
                ))}
              </div>
            </div>
            <ul className="site-probe-rail-list">
              {railFiltered.map((site) => (
                <li key={site.site_id}>
                  <button
                    type="button"
                    className={`site-probe-rail-item${
                      activeSiteId === site.site_id ? " is-active" : ""
                    }${site.probe_source_enabled ? "" : " is-off"}`}
                    onClick={() => setActiveSiteId(site.site_id)}
                  >
                    <span className="site-probe-rail-name">{site.site_name}</span>
                    <span className="site-probe-rail-meta mono">
                      {t("modelsPage.siteProbe.railCounts", {
                        attached: rowsBySite.get(site.site_id)?.length ?? 0,
                        candidates: candidatesBySite.get(site.site_id)?.length ?? 0,
                      })}
                    </span>
                    {site.probe_last_error ? (
                      <span
                        className="site-probe-rail-dot is-error"
                        title={site.probe_last_error}
                      />
                    ) : site.auto_apply ? (
                      <span className="site-probe-rail-dot is-auto" />
                    ) : null}
                  </button>
                </li>
              ))}
              {railFiltered.length === 0 ? (
                <li className="site-probe-rail-empty">{t("modelsPage.siteProbe.railNoMatch")}</li>
              ) : null}
            </ul>
          </nav>
          <div className="site-probe-detail">
            {activeSite ? (
              <SiteProbeDetail
                site={activeSite}
                rows={rowsBySite.get(activeSite.site_id) ?? []}
                candidates={candidatesBySite.get(activeSite.site_id) ?? []}
                sourcePending={busy}
                autoApplyPending={busy}
                collectPending={busy}
                onToggleSource={() => toggleSource.mutate(activeSite)}
                onToggleAutoApply={() => toggleAutoApply.mutate(activeSite)}
                onCollect={() => collectOne.mutate(activeSite.site_id)}
                onAdopt={adopt.mutate}
                onConfigure={() => {
                  pickSite(String(activeSite.site_id));
                  document.getElementById("site-probe-source-settings")?.setAttribute("open", "");
                  document
                    .getElementById("site-probe-source-settings")
                    ?.scrollIntoView?.({ block: "nearest" });
                }}
                adoptPending={adopt.isPending}
              />
            ) : (
              <Empty>{t("modelsPage.siteProbe.detailPick")}</Empty>
            )}
          </div>
        </div>
      )}

      {adopt.data ? (
        <div className="unify-result" role="status">
          {adopt.data.results.every((result) => result.skipped)
            ? describeSkip(adopt.data.results[0]?.skipped, t)
            : t("modelsPage.siteProbe.adopted")}
        </div>
      ) : null}
      {adopt.error ? <div className="inline-error">{String(adopt.error)}</div> : null}

      <section className="site-probe-changes">
        {actionsAreDryRun && actions !== null && previewFor !== evaluationKey ? (
          <p role="status" className="field-hint">
            {t("modelsPage.siteProbe.previewExpired")}
          </p>
        ) : null}
        <header className="probe-picker-head">
          <h3>{t("modelsPage.siteProbe.changes")}</h3>
          <span className="probe-picker-count">
            {actions === null
              ? t("modelsPage.siteProbe.changesIdle")
              : t("modelsPage.siteProbe.changesCount", { count: actions.length })}
          </span>
        </header>
        {actions === null ? (
          <p className="field-hint">{t("modelsPage.siteProbe.changesHint")}</p>
        ) : actions.length === 0 ? (
          <p className="field-hint">{t("modelsPage.siteProbe.noActions")}</p>
        ) : (
          <>
            <div className="unify-result" role="status">
              {t("modelsPage.siteProbe.actionSummary", {
                count: actions.length,
                mode: actionsAreDryRun
                  ? t("modelsPage.siteProbe.dryRun")
                  : t("modelsPage.siteProbe.applied"),
              })}
            </div>
            <table className="table">
              <thead>
                <tr>
                  <th>{t("modelsPage.siteProbe.colModel")}</th>
                  <th>{t("modelsPage.siteProbe.colSite")}</th>
                  <th>{t("modelsPage.siteProbe.colChannel")}</th>
                  <th>{t("modelsPage.siteProbe.colAction")}</th>
                  <th>{t("modelsPage.siteProbe.colReason")}</th>
                </tr>
              </thead>
              <tbody>
                {actions.map((action, index) => (
                  <tr key={`${action.route}-${action.channel_id}-${index}`}>
                    <td className="mono">{action.route}</td>
                    <td>{action.site_name}</td>
                    <td>{action.channel_name}</td>
                    <td>
                      {action.skipped
                        ? skipLabel(action.skipped, t)
                        : action.kind === "disable"
                          ? t("modelsPage.siteProbe.actionDisable")
                          : t("modelsPage.siteProbe.actionRecover")}
                    </td>
                    <td className="mono">{action.reason}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </>
        )}
      </section>

      <details id="site-probe-source-settings" className="advanced-section">
        <summary>{t("modelsPage.siteProbe.advanced")}</summary>
        <p className="field-hint">{t("modelsPage.siteProbe.advancedHint")}</p>
        <div className="field-row">
          <label className="field">
            <span>{t("modelsPage.siteProbe.assignSite")}</span>
            <select
              value={siteId}
              disabled={busy || detect.isPending}
              onChange={(event) => pickSite(event.target.value)}
            >
              <option value="">{t("modelsPage.siteProbe.pickSite")}</option>
              {(sites.data ?? []).map((site) => (
                <option key={site.id} value={String(site.id)}>
                  {siteLabel(site)}
                </option>
              ))}
            </select>
          </label>
          <label className="check">
            <input
              type="checkbox"
              checked={autoMode}
              onChange={(event) => setAutoMode(event.target.checked)}
            />
            <span className="setting-check-label">
              <span>{t("modelsPage.siteProbe.autoSource")}</span>
              <InfoTip label={t("modelsPage.siteProbe.autoSourceHint")} />
            </span>
          </label>
        </div>
        {!autoMode ? (
          <div className="field-row">
            <label className="field">
              <span>{t("modelsPage.siteProbe.sourceUrl")}</span>
              <input
                className="mono"
                value={url}
                placeholder="https://stat.example.com/status/ai"
                onChange={(event) => {
                  setUrl(event.target.value);
                  setDetection(null);
                }}
              />
              <span className="field-hint">{t("modelsPage.siteProbe.sourceUrlHint")}</span>
            </label>
            <div className="probe-picker-actions">
              <button
                type="button"
                className="unify-covered-toggle"
                disabled={url.trim() === "" || detect.isPending || busy}
                onClick={() => detect.mutate({ url: url.trim(), siteId })}
              >
                {detect.isPending
                  ? t("modelsPage.siteProbe.detecting")
                  : t("modelsPage.siteProbe.detect")}
              </button>
            </div>
          </div>
        ) : null}
        <div className="site-probe-policy">
          {(
            [
              ["ratio_threshold", "threshold", 100, "unitPercent"],
              ["min_samples", "minSamples", 1000, "unitSamples"],
              ["low_rounds", "lowRounds", 10, "unitRounds"],
              ["high_rounds", "highRounds", 10, "unitRounds"],
            ] as const
          ).map(([key, label, max, unit]) => (
            <label className="field site-probe-number" key={key}>
              <span>{t("modelsPage.siteProbe." + label)}</span>
              <span className="site-probe-number-input">
                <input
                  type="number"
                  min={1}
                  max={max}
                  value={
                    key === "ratio_threshold"
                      ? Math.round(sourcePolicy[key] * 100)
                      : sourcePolicy[key]
                  }
                  onChange={(event) =>
                    setSourcePolicy({
                      ...sourcePolicy,
                      [key]: Number(event.target.value) / (key === "ratio_threshold" ? 100 : 1),
                    })
                  }
                />
                <em>{t("modelsPage.siteProbe." + unit)}</em>
              </span>
            </label>
          ))}
        </div>
        {/* The four numbers in one sentence: an operator setting a threshold
            should be able to read back what it does without knowing the code. */}
        <p className="site-probe-policy-hint">
          {t("modelsPage.siteProbe.policyPreview", {
            low: sourcePolicy.low_rounds,
            threshold: Math.round(sourcePolicy.ratio_threshold * 100),
            samples: sourcePolicy.min_samples,
            high: sourcePolicy.high_rounds,
          })}
        </p>
        <p className="field-hint">{t("modelsPage.siteProbe.sourcePolicyHint")}</p>
        <div className="probe-picker-actions">
          <Button disabled={!canSave || busy || detect.isPending} onClick={saveDraft}>
            {t("modelsPage.siteProbe.save")}
          </Button>
          <Button
            variant="secondary"
            disabled={!siteId || busy}
            onClick={() => clear.mutate(Number(siteId))}
          >
            {t("modelsPage.siteProbe.clear")}
          </Button>
        </div>
        {detection ? (
          <div className="unify-result" role="status">
            {describeDetection(detection, t)}
          </div>
        ) : null}
        {detect.error ? <div className="inline-error">{String(detect.error)}</div> : null}
        {save.error ? <div className="inline-error">{String(save.error)}</div> : null}
        {/* Destructive and rare, so it is separated and armed by a second
            click instead of sitting next to Save as a red word. */}
        <div className="site-probe-maintenance">
          <span className="site-probe-maintenance-title">
            {t("modelsPage.siteProbe.maintenance")}
          </span>
          <Button
            variant="danger"
            disabled={busy}
            onClick={() => {
              if (!pruneArmed) {
                setPruneArmed(true);
                return;
              }
              setPruneArmed(false);
              catalogPrune.mutate();
            }}
          >
            {pruneArmed ? t("modelsPage.siteProbe.pruneConfirm") : t("modelsPage.siteProbe.prune")}
          </Button>
          {catalogPrune.data ? (
            <span className="field-hint">
              {t("modelsPage.siteProbe.pruned", {
                removed: catalogPrune.data.removed,
              })}
            </span>
          ) : null}
          <span className="field-hint">{t("modelsPage.siteProbe.pruneHint")}</span>
        </div>
        {unmatchedRows.length > 0 ? (
          <details className="advanced-section">
            <summary>
              {t("modelsPage.siteProbe.unmatched", {
                count: unmatchedRows.length,
              })}
            </summary>
            <p className="field-hint">{t("modelsPage.siteProbe.unmatchedHint")}</p>
            {/* 158 rows of raw list is not a report. Summary first, then a search,
                then the first page — the operator is looking for one model, not
                reading the site's catalogue. */}
            <div className="probe-picker-head">
              <span className="site-probe-summary" role="status">
                {t("modelsPage.siteProbe.unmatchedSummary", {
                  count: unmatchedFiltered.length,
                  sites: new Set(unmatchedFiltered.map((row) => row.site_id)).size,
                })}
              </span>
              <div className="probe-search">
                <Search size={13} />
                <input
                  type="search"
                  value={unmatchedQuery}
                  placeholder={t("modelsPage.siteProbe.search")}
                  onChange={(event) => {
                    setUnmatchedQuery(event.target.value);
                    setShowAllUnmatched(false);
                  }}
                />
              </div>
            </div>
            <ul className="site-probe-unmatched-list">
              {unmatchedShown.map((row) => (
                <li key={`${row.site_id}-${row.raw_model}`}>
                  <span className="mono">{row.raw_model}</span>
                  <span className="site-probe-unmatched-site">{row.site_name}</span>
                  <span className="mono">{formatRatio(row.ratio, row.samples, t)}</span>
                  <span className="mono">{formatPrice(row.price, t)}</span>
                  <span className="field-hint">{t("modelsPage.siteProbe.unmatchedReason")}</span>
                </li>
              ))}
            </ul>
            {unmatchedFiltered.length > UNMATCHED_PAGE && !showAllUnmatched ? (
              <button
                type="button"
                className="unify-covered-toggle"
                onClick={() => setShowAllUnmatched(true)}
              >
                {t("modelsPage.siteProbe.showAll", { count: unmatchedFiltered.length })}
              </button>
            ) : null}
            {showAllUnmatched && unmatchedFiltered.length > UNMATCHED_PAGE ? (
              <button
                type="button"
                className="unify-covered-toggle"
                onClick={() => setShowAllUnmatched(false)}
              >
                {t("modelsPage.siteProbe.showLess")}
              </button>
            ) : null}
          </details>
        ) : null}
      </details>
    </Dialog>
  );
}

/**
 * One site's detail: its collection state, its two switches, the candidates it
 * publishes, and the models it reports.
 *
 * The switches live here because the site is the unit they act on — a site whose
 * price page changed is turned off here, without touching any other site or any
 * routing table.
 */
function SiteProbeDetail({
  site,
  rows,
  candidates,
  sourcePending,
  autoApplyPending,
  collectPending,
  onToggleSource,
  onConfigure,
  onToggleAutoApply,
  onCollect,
  onAdopt,
  adoptPending,
}: {
  site: SiteProbeSiteStatus;
  rows: SiteProbeRow[];
  candidates: SiteProbeNameOnly[];
  sourcePending: boolean;
  autoApplyPending: boolean;
  collectPending: boolean;
  onToggleSource: () => void;
  onConfigure: () => void;
  onToggleAutoApply: () => void;
  onCollect: () => void;
  onAdopt: (memberIds: number[]) => void;
  adoptPending: boolean;
}) {
  const { t } = useI18n();
  const low = rows.filter((row) => row.verdict === "low").length;
  const parked = rows.reduce(
    (total, row) => total + (row.members ?? []).filter((member) => member.auto_disabled).length,
    0,
  );
  const status = site.probe_last_error
    ? t("modelsPage.siteProbe.collectFailed")
    : site.probe_last_run_at
      ? t("modelsPage.siteProbe.collectOk", { count: site.monitor_count })
      : t("modelsPage.siteProbe.neverCollected");
  return (
    <section className={`site-probe-card${site.probe_source_enabled ? "" : " is-off"}`}>
      <header className="site-probe-card-head">
        <div className="site-probe-card-title">
          <span className="site-probe-card-name">{site.site_name}</span>
          <span className="site-probe-card-kind">
            {site.probe_source_enabled
              ? sourceKindLabel(site.probe_source_kind, t)
              : t("modelsPage.siteProbe.noSource")}
          </span>
        </div>
        <div className="site-probe-card-switches">
          <Button variant="quiet" disabled={sourcePending} onClick={onConfigure}>
            {t("modelsPage.siteProbe.configure")}
          </Button>
          <button
            type="button"
            className={`site-probe-switch${site.probe_source_enabled ? " is-on" : ""}`}
            aria-pressed={site.probe_source_enabled}
            disabled={sourcePending}
            title={t("modelsPage.siteProbe.sourceOnHint")}
            onClick={onToggleSource}
          >
            {site.probe_source_enabled
              ? t("modelsPage.siteProbe.sourceOn")
              : t("modelsPage.siteProbe.sourceOff")}
          </button>
          <button
            type="button"
            className={`site-probe-switch${site.auto_apply ? " is-on" : ""}`}
            aria-pressed={site.auto_apply}
            disabled={autoApplyPending || !site.probe_source_enabled}
            title={t("modelsPage.siteProbe.autoApplyHint")}
            onClick={onToggleAutoApply}
          >
            {site.auto_apply
              ? t("modelsPage.siteProbe.autoApplyOn")
              : t("modelsPage.siteProbe.autoApplyOff")}
          </button>
        </div>
      </header>
      {/* The switch in the header is the whole per-site decision, so what it does
          is spelled out where the switch is. It used to sit at the bottom of
          高级设置 as a second control for the same flag, where the operator
          reading thresholds met it long after flipping the toggle. */}
      {site.probe_source_enabled ? (
        <p className={`site-probe-auto-state${site.auto_apply ? " is-on" : ""}`}>
          {t(
            site.auto_apply
              ? "modelsPage.siteProbe.autoApplyOnHint"
              : "modelsPage.siteProbe.autoApplyOffHint",
          )}
        </p>
      ) : null}
      <div className="site-probe-card-meta">
        <span>
          {t("modelsPage.siteProbe.cardMeta", {
            models: rows.length,
            low,
            parked,
          })}
        </span>
        <span className="site-probe-card-run">
          {status}
          {site.probe_last_run_at ? ` · ${formatTime(site.probe_last_run_at)}` : ""}
        </span>
        <button
          type="button"
          className="site-probe-collect"
          disabled={collectPending || !site.probe_source_enabled}
          onClick={onCollect}
        >
          {collectPending
            ? t("modelsPage.siteProbe.collecting")
            : t("modelsPage.siteProbe.collectNow")}
        </button>
      </div>
      {site.probe_last_error ? <div className="inline-error">{site.probe_last_error}</div> : null}
      {candidates.length > 0 ? (
        <details className="site-probe-candidates">
          <summary>{t("modelsPage.siteProbe.candidates", { count: candidates.length })}</summary>
          <p className="field-hint">{t("modelsPage.siteProbe.candidatesHint")}</p>
          <ul className="site-probe-candidate-list">
            {candidates.map((candidate, index) => (
              <li key={`${candidate.raw_model}-${candidate.route}-${index}`}>
                <span className="mono">{candidate.raw_model}</span>
                <span className="site-probe-route-chip mono">
                  {t("modelsPage.siteProbe.routeChip", { route: candidate.route })}
                </span>
                <span className="mono site-probe-candidate-reading">
                  {formatRatio(candidate.ratio, candidate.samples, t)}
                </span>
                <span className="mono">{formatPrice(candidate.price, t)}</span>
              </li>
            ))}
          </ul>
        </details>
      ) : null}
      {rows.length === 0 ? (
        <p className="field-hint site-probe-card-empty">
          {site.probe_source_enabled
            ? t("modelsPage.siteProbe.cardNoReadings")
            : t("modelsPage.siteProbe.cardOff")}
        </p>
      ) : (
        <div className="site-probe-models">
          <ModelListHeader />
          <ul className="site-probe-model-list">
            {rows.map((row, index) => (
              <ModelLine
                key={`${row.route}-${row.site_id}-${row.raw_model}-${row.group_name}-${index}`}
                row={row}
                onAdopt={onAdopt}
                adoptPending={adoptPending}
              />
            ))}
          </ul>
        </div>
      )}
    </section>
  );
}

/**
 * One model inside a site's list.
 *
 * The title is the model the SITE publishes, not the route it happens to match.
 * Those are different things and conflating them is how a table of unrelated
 * readings ends up looking like one model repeated: the match index is a name
 * index, so a dozen sites publishing "kimi-k3" all rendered under that route's
 * name. The route follows as a chip, so the connection stays visible without
 * pretending the names are the same.
 *
 * The availability cell always says WHERE its number came from: a site's own
 * status page, a third-party directory and our own relayed traffic are different
 * evidence, and a screen that mixes them silently cannot be trusted to park a
 * channel.
 */
function ModelLine({
  row,
  onAdopt,
  adoptPending,
}: {
  row: SiteProbeRow;
  onAdopt: (memberIds: number[]) => void;
  adoptPending: boolean;
}) {
  const { t } = useI18n();
  const newest = row.rounds[0];
  const parked = (row.members ?? []).some((member) => member.auto_disabled);
  return (
    <li className="site-probe-model">
      <div className="site-probe-model-name">
        <span className="mono" title={row.raw_model}>
          {row.raw_model}
        </span>
        {row.route && row.route !== row.raw_model ? (
          <span className="site-probe-route-chip mono">
            {t("modelsPage.siteProbe.routeChip", { route: row.route })}
          </span>
        ) : null}
        <span className={`site-probe-pill is-${row.verdict}`}>
          {t(`modelsPage.siteProbe.verdict.${row.verdict}`)}
        </span>
        {row.match && row.match !== "exact" ? (
          <span className="site-probe-match">{t(`modelsPage.siteProbe.match.${row.match}`)}</span>
        ) : null}
        {newest?.weak_evidence && row.availability_source !== "traffic" ? (
          <span className="site-probe-tag">{t("modelsPage.siteProbe.weakEvidence")}</span>
        ) : null}
      </div>
      <span className="site-probe-cell is-value">
        {row.availability_source === "traffic" && row.traffic ? (
          <>
            <span className="mono">{formatRatio(row.traffic.ratio, row.traffic.samples, t)}</span>
            <span className="site-probe-tag">
              {t("modelsPage.siteProbe.sourceTraffic", { hours: row.traffic.window_hours })}
            </span>
          </>
        ) : row.availability_source === "watchbot" && row.external ? (
          <>
            <span className="mono">{formatPercent(row.external.ratio)}</span>
            <span className="site-probe-tag">
              {t("modelsPage.siteProbe.sourceExternal", { source: row.external.source })}
            </span>
          </>
        ) : newest && newest.samples > 0 ? (
          <>
            <span className="mono">{formatAvailability(newest.up_count, newest.samples, t)}</span>
            <span className="site-probe-tag">{t("modelsPage.siteProbe.sourceSite")}</span>
          </>
        ) : (
          <span className="mono">{t("modelsPage.siteProbe.noSamples")}</span>
        )}
      </span>
      <span className="site-probe-cell is-value">
        <span className="mono">
          {row.low_streak > 0
            ? t("modelsPage.siteProbe.streakLow", { count: row.low_streak })
            : row.ok_streak > 0
              ? t("modelsPage.siteProbe.streakOk", { count: row.ok_streak })
              : "—"}
        </span>
      </span>
      <span className="site-probe-cell is-value">
        <span className="mono">
          {row.observed_price ? formatPrice(row.observed_price, t) : "—"}
        </span>
        {row.catalog_price ? (
          <span className="site-probe-tag">
            {t("modelsPage.siteProbe.catalogPrice", {
              price: formatPrice(row.catalog_price, t),
            })}
          </span>
        ) : null}
      </span>
      <span className="site-probe-cell is-value">
        {(row.members ?? []).map((member) => (
          <span
            key={member.member_id}
            className={`site-probe-member${member.auto_disabled ? " is-parked" : ""}${
              member.enabled ? "" : " is-off"
            }`}
            title={member.channel_name}
          >
            {member.single_member
              ? t("modelsPage.siteProbe.memberSingle", { name: member.channel_name })
              : member.auto_disabled
                ? t("modelsPage.siteProbe.memberAutoDisabled", { name: member.channel_name })
                : member.enabled
                  ? member.channel_name
                  : t("modelsPage.siteProbe.memberDisabled", { name: member.channel_name })}
          </span>
        ))}
        {parked && row.verdict !== "low" ? (
          <span className="site-probe-hint">{t("modelsPage.siteProbe.parkedHint")}</span>
        ) : null}
      </span>
      <span className="site-probe-cell is-action">
        {adoptablePrice(row) ? (
          <button
            type="button"
            className="site-probe-adopt"
            disabled={adoptPending}
            onClick={() => onAdopt((row.members ?? []).map((member) => member.member_id))}
          >
            <ChevronRight size={12} />
            {t("modelsPage.siteProbe.adoptPrice")}
          </button>
        ) : null}
      </span>
    </li>
  );
}

/** Column labels for a site's model list; the values below are right-aligned. */
function ModelListHeader() {
  const { t } = useI18n();
  return (
    <div className="site-probe-model site-probe-model-head" aria-hidden="true">
      <span>{t("modelsPage.siteProbe.colModel")}</span>
      <span>{t("modelsPage.siteProbe.colAvailability")}</span>
      <span>{t("modelsPage.siteProbe.colStreak")}</span>
      <span>{t("modelsPage.siteProbe.colPrice")}</span>
      <span>{t("modelsPage.siteProbe.colChannel")}</span>
      <span />
    </div>
  );
}

/** A row has evidence when either the site or our own traffic said something. */
function rowHasEvidence(row: SiteProbeRow): boolean {
  if ((row.traffic?.samples ?? 0) > 0) return true;
  return (row.rounds ?? []).some((round) => round.samples > 0);
}

function siteLabel(site: Site): string {
  return site.probe_source_enabled && site.probe_source_kind
    ? `${site.name} · ${site.probe_source_kind}`
    : site.name;
}

/** A price can be adopted when it parsed, is in USD (the currency the billing
 * columns hold), and the row serves a member. A non-USD quote is not adoptable
 * at all: writing a ¥ amount into a dollars-per-1k column would silently
 * misprice every request that member serves. */
function adoptablePrice(row: SiteProbeRow): boolean {
  const currency = (row.observed_price?.currency ?? "").toUpperCase();
  return (
    !!row.observed_price &&
    !row.observed_price.unparsed &&
    (currency === "" || currency === "USD") &&
    (row.members ?? []).length > 0
  );
}

/** A skip code we have no copy for is shown raw rather than hidden. */
function describeSkip(skipped: string | undefined, t: Translate): string {
  if (!skipped) return "";
  const label = t(`modelsPage.siteProbe.skip.${skipped}`);
  return label === `modelsPage.siteProbe.skip.${skipped}` ? skipped : label;
}

function sourceKindLabel(kind: string | undefined, t: Translate): string {
  if (kind === "uptime_kuma") return t("modelsPage.siteProbe.kindUptimeKuma");
  if (kind === "newapi") return t("modelsPage.siteProbe.kindNewApi");
  if (kind === "sub2api_transit") return t("modelsPage.siteProbe.kindSub2Api");
  return kind ?? "—";
}

/** Translate type shared by the helpers below. */
type Translate = (key: string, values?: Record<string, string | number>) => string;

/** A skip code we have no copy for is shown raw rather than hidden. */
function skipLabel(skipped: string, t: Translate): string {
  const key = `modelsPage.siteProbe.skip.${skipped}`;
  const label = t(key);
  return label === key ? skipped : label;
}

function describeDetection(detection: SiteProbeDetection, t: Translate): string {
  if (detection.kind === "uptime_kuma") {
    const models = (detection.monitors ?? []).filter((monitor) => monitor.samples > 0).length;
    return t("modelsPage.siteProbe.detectedKuma", {
      title: detection.title ?? detection.slug ?? "",
      groups: detection.groups?.length ?? 0,
      monitors: detection.monitors?.length ?? 0,
      models,
    });
  }
  if (detection.kind === "sub2api_transit") {
    return t("modelsPage.siteProbe.detectedSub2Api", {
      mode: detection.transit_mode
        ? t(`modelsPage.siteProbe.transitMode.${detection.transit_mode}`)
        : "",
      count: detection.transit_readings?.length ?? 0,
    });
  }
  return t("modelsPage.siteProbe.detectedNewApi", {
    count: detection.price_count ?? 0,
    unparsed: detection.price_unparsed ?? 0,
  });
}

function formatAvailability(up: number, samples: number, t: Translate): string {
  if (samples <= 0) return t("modelsPage.siteProbe.noSamples");
  const percent = Math.round((up / samples) * 1000) / 10;
  return `${percent}% (${up}/${samples})`;
}

function formatRatio(ratio: number, samples: number, t: Translate): string {
  if (samples <= 0) return t("modelsPage.siteProbe.noSamples");
  return `${Math.round(ratio * 1000) / 10}% (${samples})`;
}

function formatPercent(ratio: number): string {
  return `${Math.round(ratio * 1000) / 10}%`;
}

// formatPrice prints a published price in the currency the site declared.
//
// It deliberately does NOT convert: the site's own table is denominated in
// whatever it says (New-API's quota_display_type) — one site is USD, the next is
// CNY — and the gateway has no verified exchange rate for them. A number
// carrying the wrong currency symbol is worse than one carrying its own.
function formatPrice(price: SiteProbePrice | undefined, t: Translate): string {
  if (!price) return "—";
  if (price.unparsed) {
    return t("modelsPage.siteProbe.priceUnparsed", {
      raw: price.raw ?? "",
    });
  }
  const unit = price.currency_symbol
    ? price.currency_symbol
    : price.currency
      ? `${price.currency} `
      : "";
  const amount = (value: number | undefined) =>
    value === undefined || !Number.isFinite(value)
      ? "—"
      : `${unit}${Number(value.toPrecision(10))}`;
  if (price.mode === "fixed") {
    return t("modelsPage.siteProbe.perCallPrice", {
      price: amount(price.per_request),
    });
  }
  return t("modelsPage.siteProbe.perMillionPrice", {
    input: amount(price.input_per_million),
    output: amount(price.output_per_million),
  });
}

function formatTime(value: string): string {
  const normalized = value.replace(" ", "T");
  const date = new Date(
    /(?:Z|[+-]\d{2}:?\d{2})$/i.test(normalized) ? normalized : normalized + "Z",
  );
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}
