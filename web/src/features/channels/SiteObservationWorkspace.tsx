import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Link } from "react-router-dom";
import { Download, RefreshCw, Search, Settings2 } from "lucide-react";
import { api } from "../../api/client";
import type {
  SiteObservation,
  SiteProbeAction,
  SiteProbePolicy,
  SiteProbeRun,
  SiteProbeSiteStatus,
  Site,
} from "../../api/types";
import { AvailabilityIndicator } from "../../components/AvailabilityIndicator";
import { Button, ConfirmDialog, Dialog, Empty, ErrorState, InfoTip } from "../../components/ui";
import { ActionMenu } from "../../components/ActionMenu";
import { useAdminMutation } from "../../hooks/useAdminMutation";
import { useI18n } from "../../i18n";
import { useSession } from "../../session";
import { useToast } from "../../toast";
import { BillingRuleDialog } from "../models/BillingRulePanel";

const DEFAULT_POLICY: SiteProbePolicy = {
  ratio_threshold: 0.9,
  min_samples: 5,
  low_rounds: 2,
  high_rounds: 2,
};
export const CATALOG_URL = "https://watchbot.cfd/api/v1/public/dashboard";
const INVALIDATE = [
  ["site-observations"],
  ["site-probe"],
  ["availability"],
  ["sites"],
  ["route-overviews"],
  ["channels"],
];
export function validObservationPolicy(value: SiteProbePolicy) {
  return (
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
    value.high_rounds <= 10
  );
}
export function formatObservedPrice(price: SiteObservation["price"]) {
  if (!price.mode && !price.unparsed) return "—";
  if (price.unparsed) return price.raw || "—";
  const currency =
    price.unit?.kind === "site_credit" ? "" : price.currency_symbol || price.currency || "";
  const amount = (value: number | undefined) =>
    value == null ? "—" : `${currency}${Number(value.toPrecision(8))}`;
  return price.mode === "fixed"
    ? amount(price.per_request)
    : `${amount(price.input_per_million)} / ${amount(price.output_per_million)}`;
}

export function SiteObservationWorkspace({
  initialSiteId,
  initialModel = "",
}: {
  initialSiteId?: number;
  initialModel?: string;
}) {
  const { client } = useSession();
  const { t } = useI18n();
  const toast = useToast();
  const service = api(client!);
  const [hours, setHours] = useState(24);
  const [query, setQuery] = useState(initialModel);
  const [selectedId, setSelectedId] = useState<number | undefined>(initialSiteId);
  const [sourceSite, setSourceSite] = useState<Site | null>(null);
  const [filter, setFilter] = useState<"all" | "issues" | "data">("all");
  const [policy, setPolicy] = useState(DEFAULT_POLICY);
  const [preview, setPreview] = useState<{
    actions: SiteProbeAction[];
    key: string;
    applied: boolean;
  } | null>(null);
  const [pruneOpen, setPruneOpen] = useState(false);
  const observations = useQuery({
    queryKey: ["site-observations", hours],
    queryFn: ({ signal }) => service.siteObservations(hours, signal),
    refetchInterval: 60_000,
  });
  const sites = useQuery({ queryKey: ["sites"], queryFn: ({ signal }) => service.sites(signal) });
  const runtime = useQuery({
    queryKey: ["runtime-settings"],
    queryFn: ({ signal }) => service.runtimeSettings(signal),
  });
  const report = useQuery({
    queryKey: ["site-probe", policy],
    queryFn: ({ signal }) => service.siteProbeReport(policy, signal),
  });
  const [routeRowsOpen, setRouteRowsOpen] = useState(false);
  const [importOpen, setImportOpen] = useState(false);
  // The scope of the last collect, so the retry under a failure banner repeats
  // the same request (one site or the whole fleet) instead of widening it.
  const collectScope = useRef<number[] | undefined>(undefined);

  // "0 collected" is the answer an operator cannot act on. Name the state
  // instead: collection off, the last round failed (the upstream's own message
  // is above), never collected, or collected fine with an empty catalogue. The
  // window case is the one that misleads most — the site did publish models,
  // just not inside the selected period.
  const zeroReason = (site: SiteProbeSiteStatus | undefined): string | null => {
    if (!site) return null;
    if (!site.probe_source_enabled) return t("observation.zero.sourceOff");
    if (site.probe_last_error) return t("observation.zero.failed");
    if (!site.probe_last_run_at) return t("observation.zero.never");
    if (site.last_run_status === "running") return t("observation.collecting");
    if (site.last_run_status === "ok" && site.monitor_count === 0)
      return t("observation.zero.emptyCatalogue");
    if (site.monitor_count > 0)
      return t("observation.zero.outsideWindow", { count: site.monitor_count });
    return t("observation.zero.unknown");
  };

  /** One line for what a finished round did — the count a click used to swallow. */
  const collectOutcome = (data: {
    runs?: SiteProbeRun[];
    collected?: number;
    failed: number;
  }): string => {
    const runs = data.runs ?? [];
    if (runs.length === 1) {
      const run = runs[0]!;
      const name =
        (observations.data?.sites ?? []).find((site) => site.site_id === run.site_id)?.site_name ??
        `#${run.site_id}`;
      if (run.status === "failed")
        return t("observation.collectSiteFailed", {
          name,
          reason: run.error || t("observation.zero.unknown"),
        });
      if (run.monitor_count === 0) return t("observation.collectSiteEmpty", { name });
      return t("observation.collectSite", { name, count: run.monitor_count });
    }
    if (runs.length > 1) {
      return t("observation.collectRound", {
        collected: runs.filter((run) => run.status === "ok").length,
        failed: data.failed,
      });
    }
    return t("observation.collectRound", { collected: data.collected ?? 0, failed: data.failed });
  };

  /** The directory's own receipt: what it matched, left alone, and collected. */
  const importOutcome = (data: {
    matched: number;
    skipped: number;
    unmatched: number;
    created: number;
    collected?: number;
    failed?: number;
  }): string =>
    t("observation.imported", {
      matched: data.matched,
      skipped: data.skipped,
      unmatched: data.unmatched,
      created: data.created,
      collected: data.collected ?? 0,
      failed: data.failed ?? 0,
    });

  const collect = useAdminMutation({
    mutationFn: (ids?: number[]) => service.siteProbeCollect(ids),
    invalidateKeys: INVALIDATE,
    onSuccess: (data, ids) => {
      collectScope.current = ids;
      toast.push({
        tone: data.failed > 0 ? "error" : "success",
        message: collectOutcome(data),
      });
    },
    onError: (_error, ids) => {
      collectScope.current = ids;
    },
  });
  const importCatalog = useAdminMutation({
    mutationFn: () => service.siteProbeCatalogImport({ url: CATALOG_URL }),
    invalidateKeys: INVALIDATE,
    onSuccess: (data) => {
      setImportOpen(false);
      toast.push({ tone: "success", message: importOutcome(data) });
    },
  });
  const prune = useAdminMutation({
    mutationFn: () => service.siteProbeCatalogPrune(),
    invalidateKeys: INVALIDATE,
    onSuccess: () => setPruneOpen(false),
  });
  const toggle = useAdminMutation({
    mutationFn: ({ site, automatic }: { site: SiteProbeSiteStatus; automatic: boolean }) => {
      const stored = sites.data?.find((item) => item.id === site.site_id);
      return service.saveSiteProbeSource({
        site_id: site.site_id,
        kind: stored?.probe_source_kind ?? "",
        url: stored?.probe_source_url ?? "",
        auto: stored?.probe_auto ?? true,
        enabled: automatic ? site.probe_source_enabled : !site.probe_source_enabled,
        config: JSON.stringify({
          auto_apply: automatic ? !site.auto_apply : site.auto_apply,
          policy: site.policy,
        }),
      });
    },
    invalidateKeys: INVALIDATE,
  });
  const [billing, setBilling] = useState<{
    model: string;
    memberId: number;
    siteId: number;
    upstreamModel: string;
    channelName?: string;
  } | null>(null);
  const selected =
    (observations.data?.sites ?? []).find((item) => item.site_id === selectedId) ??
    observations.data?.sites[0];
  const previewKey = JSON.stringify([selected?.site_id, policy, report.dataUpdatedAt]);
  const previewKeyRef = useRef(previewKey);
  previewKeyRef.current = previewKey;
  const apply = useAdminMutation({
    mutationFn: (input: { dry: boolean; siteId: number; key: string; policy: SiteProbePolicy }) =>
      service.siteProbeApply({
        site_ids: [input.siteId],
        policy: input.policy,
        dry_run: input.dry,
      }),
    invalidateKeys: [["route-overviews"], ["channels"]],
    onSuccess: (data, input) => {
      if (input.key === previewKeyRef.current) {
        setPreview({ actions: data.actions, key: input.key, applied: !input.dry });
      }
    },
  });
  const busy = collect.isPending || importCatalog.isPending || toggle.isPending || apply.isPending;
  useEffect(() => {
    setSelectedId(initialSiteId);
    setQuery(initialModel);
  }, [initialSiteId, initialModel]);
  useEffect(() => {
    setPreview(null);
  }, [selected?.site_id, policy]);
  const items = useMemo(() => observations.data?.items ?? [], [observations.data]);
  const term = query.trim().toLowerCase();
  const siteRows = (observations.data?.sites ?? []).filter((site) => {
    const models = items.filter((item) => item.site_id === site.site_id);
    if (
      filter === "issues" &&
      !site.probe_last_error &&
      !models.some((model) => model.series.failed > 0 || model.series.stale)
    )
      return false;
    if (filter === "data" && models.length === 0) return false;
    return (
      !term ||
      site.site_name.toLowerCase().includes(term) ||
      models.some((model) => model.raw_model.toLowerCase().includes(term))
    );
  });
  const visible = items.filter(
    (item) =>
      item.site_id === selected?.site_id &&
      (!term ||
        item.raw_model.toLowerCase().includes(term) ||
        selected?.site_name.toLowerCase().includes(term)),
  );
  const links = (report.data?.rows ?? []).filter((row) => row.site_id === selected?.site_id);
  const siteModels = selected
    ? items.filter((item) => item.site_id === selected.site_id).length
    : 0;
  const reason = siteModels === 0 ? zeroReason(selected) : null;
  return (
    <section className="site-observation-workspace">
      <div className="observation-toolbar">
        <label className="observation-search">
          <Search size={15} />
          <input
            type="search"
            value={query}
            onChange={(event) => setQuery(event.target.value)}
            placeholder={t("observation.search")}
            aria-label={t("observation.search")}
          />
        </label>
        <div className="observation-period" role="group" aria-label={t("observation.period")}>
          {[24, 168].map((value) => (
            <button
              type="button"
              key={value}
              aria-pressed={hours === value}
              onClick={() => setHours(value)}
            >
              {t(value === 24 ? "observation.day" : "observation.week")}
            </button>
          ))}
        </div>
        <Button
          variant="quiet"
          icon={<Download size={14} />}
          disabled={busy}
          onClick={() => setImportOpen(true)}
        >
          {importCatalog.isPending ? t("observation.importing") : t("observation.import")}
        </Button>
        <Button
          variant="secondary"
          icon={<RefreshCw size={14} />}
          disabled={busy}
          onClick={() => collect.mutate(undefined)}
        >
          {collect.isPending ? t("observation.collecting") : t("observation.collectAll")}
        </Button>
      </div>
      {runtime.data?.editable?.site_probe_interval_seconds ? (
        <p className="observation-cadence">
          {t("observation.cadence", { seconds: runtime.data.editable.site_probe_interval_seconds })}{" "}
          <Link to="/settings?tab=runtime">{t("observation.schedule")}</Link>
        </p>
      ) : null}
      {collect.error ? (
        <ErrorState error={collect.error} retry={() => collect.mutate(collectScope.current)} />
      ) : null}
      {importCatalog.data ? (
        <p role="status" className="field-hint">
          {importOutcome(importCatalog.data)}
        </p>
      ) : null}
      {observations.isPending ? (
        <Empty>{t("common.loading")}</Empty>
      ) : observations.isError ? (
        <ErrorState error={observations.error} retry={() => void observations.refetch()} />
      ) : (
        <div className="observation-layout">
          <nav className="observation-sites" aria-label={t("observation.sites")}>
            <div className="observation-site-filters">
              {(["all", "issues", "data"] as const).map((value) => (
                <button
                  type="button"
                  key={value}
                  aria-pressed={filter === value}
                  onClick={() => setFilter(value)}
                >
                  {t(`observation.filter.${value}`)}
                </button>
              ))}
            </div>
            {siteRows.map((site) => (
              <button
                type="button"
                key={site.site_id}
                className={`observation-site${site.site_id === selected?.site_id ? " is-selected" : ""}`}
                aria-current={site.site_id === selected?.site_id ? "true" : undefined}
                onClick={() => setSelectedId(site.site_id)}
              >
                <strong>{site.site_name}</strong>
                <span>
                  {t("observation.count", {
                    count: items.filter((item) => item.site_id === site.site_id).length,
                  })}
                </span>
                <i
                  className={
                    site.probe_last_error
                      ? "is-failed"
                      : site.probe_source_enabled
                        ? "is-enabled"
                        : "is-off"
                  }
                  aria-label={
                    site.probe_last_error
                      ? t("observation.collectionFailed")
                      : site.probe_source_enabled
                        ? t("observation.collectionOn")
                        : t("observation.collectionOff")
                  }
                />
              </button>
            ))}
            {!siteRows.length ? <p className="field-hint">{t("observation.noSites")}</p> : null}
          </nav>
          <div className="observation-detail">
            {selected ? (
              <>
                <header className="observation-detail-head">
                  <div>
                    <h2>{selected.site_name}</h2>
                    <p>
                      {t(`observation.kind.${selected.probe_source_kind || "none"}`)}
                      <span> · </span>
                      {selected.probe_last_run_at
                        ? new Date(selected.probe_last_run_at).toLocaleString()
                        : t("modelsPage.siteProbe.neverCollected")}
                    </p>
                  </div>
                  <div className="observation-actions">
                    <Button
                      variant="quiet"
                      icon={<Settings2 size={14} />}
                      disabled={busy}
                      onClick={() =>
                        setSourceSite(
                          sites.data?.find((site) => site.id === selected.site_id) ?? null,
                        )
                      }
                    >
                      {t("observation.source")}
                    </Button>
                    <Button
                      variant="secondary"
                      disabled={busy || !selected.probe_source_enabled}
                      onClick={() => collect.mutate([selected.site_id])}
                    >
                      {collect.isPending
                        ? t("observation.collecting")
                        : t("modelsPage.siteProbe.collectNow")}
                    </Button>
                  </div>
                </header>
                <div className="observation-switches">
                  <label className="check marginless">
                    <input
                      className="switch"
                      type="checkbox"
                      checked={selected.probe_source_enabled}
                      disabled={busy || sites.isPending || sites.isError}
                      onChange={() => toggle.mutate({ site: selected, automatic: false })}
                    />
                    <span>{t("observation.collectSwitch")}</span>
                  </label>
                  <label className="check marginless">
                    <input
                      className="switch"
                      type="checkbox"
                      checked={selected.auto_apply}
                      disabled={
                        busy || sites.isPending || sites.isError || !selected.probe_source_enabled
                      }
                      onChange={() => toggle.mutate({ site: selected, automatic: true })}
                    />
                    <span>{t("observation.autoAdjust")}</span>
                  </label>
                  <InfoTip label={t("observation.autoAdjustHint")} />
                </div>
                {toggle.error ? <ErrorState error={toggle.error} /> : null}
                {selected.probe_last_error ? (
                  <p className="observation-collection-error" role="status">
                    {t("observation.retainedSnapshot")}
                    <br />
                    {selected.probe_last_error}
                  </p>
                ) : null}
                <details className="observation-catalogue" open>
                  <summary>
                    {siteModels > 0
                      ? t("observation.collected", { count: siteModels })
                      : t("observation.collectedNone")}
                    {query.trim() ? (
                      <span className="observation-filter-count">
                        {" "}
                        · {t("observation.filteredCount", { count: visible.length })}
                      </span>
                    ) : null}
                  </summary>
                  {reason ? (
                    <p className="observation-zero-reason" role="status">
                      {reason}
                    </p>
                  ) : null}
                  <p className="observation-list-hint">{t("observation.listHint")}</p>
                  <div className="observation-table">
                    <div className="observation-model is-header">
                      <span>{t("common.model")}</span>
                      <span>{t("observation.availability")}</span>
                      <span>{t("modelsPage.siteProbe.colPrice")}</span>
                      <span>{t("observation.links")}</span>
                    </div>
                    {visible.map((item) => (
                      <ObservationRow
                        key={item.id}
                        item={item}
                        links={links.filter(
                          (row) =>
                            row.raw_model === item.raw_model &&
                            (!row.group_name || row.group_name === item.group_name),
                        )}
                        onOpenBilling={(member) =>
                          setBilling({
                            model: member.route,
                            memberId: member.member_id,
                            siteId: item.site_id,
                            upstreamModel: item.raw_model,
                            // Without the channel name the member panel explains
                            // its scope as “only affects 「」”, which names nothing.
                            channelName: member.channel_name,
                          })
                        }
                      />
                    ))}
                    {!visible.length && siteModels > 0 ? (
                      <p className="field-hint">
                        {t("observation.noMatch", { query: query.trim() })}
                      </p>
                    ) : null}
                  </div>
                </details>

                <details
                  className="observation-policy"
                  open={routeRowsOpen}
                  onToggle={(event) => setRouteRowsOpen(event.currentTarget.open)}
                >
                  <summary>{t("observation.routeImpact")}</summary>
                  <p className="field-hint">{t("observation.routeImpactHint")}</p>
                  <PolicyFields value={policy} onChange={setPolicy} disabled={busy} />
                  <div className="observation-actions">
                    <Button
                      variant="secondary"
                      disabled={
                        busy ||
                        report.isFetching ||
                        report.isError ||
                        !validObservationPolicy(policy)
                      }
                      onClick={() =>
                        apply.mutate({
                          dry: true,
                          siteId: selected.site_id,
                          key: previewKey,
                          policy,
                        })
                      }
                    >
                      {t("modelsPage.siteProbe.preview")}
                    </Button>
                    <Button
                      disabled={
                        busy ||
                        report.isFetching ||
                        report.isError ||
                        !preview ||
                        preview.applied ||
                        preview.key !== previewKey ||
                        !preview.actions.some((action) => !action.skipped)
                      }
                      onClick={() =>
                        apply.mutate({
                          dry: false,
                          siteId: selected.site_id,
                          key: previewKey,
                          policy,
                        })
                      }
                    >
                      {t("modelsPage.siteProbe.apply")}
                    </Button>
                  </div>
                  {apply.error ? <ErrorState error={apply.error} /> : null}
                  {preview ? (
                    <div className="observation-impact-results" role="status">
                      {preview.actions.length ? (
                        preview.actions.map((action, index) => (
                          <p key={`${action.channel_id}-${index}`}>
                            <strong>{action.channel_name}</strong> · {action.route} ·{" "}
                            {t(
                              `modelsPage.siteProbe.action${action.kind === "disable" ? "Disable" : "Recover"}`,
                            )}
                            {action.skipped ? ` · ${action.skipped}` : ""}
                            <small>{action.reason}</small>
                          </p>
                        ))
                      ) : (
                        <p>{t("modelsPage.siteProbe.noActions")}</p>
                      )}
                    </div>
                  ) : null}
                </details>
              </>
            ) : (
              <Empty>{t("observation.noSites")}</Empty>
            )}
          </div>
        </div>
      )}
      <details className="observation-maintenance">
        <summary>{t("modelsPage.siteProbe.maintenance")}</summary>
        <Button variant="quiet" disabled={busy} onClick={() => setPruneOpen(true)}>
          {t("modelsPage.siteProbe.prune")}
        </Button>
        <p className="field-hint">{t("modelsPage.siteProbe.pruneHint")}</p>
      </details>
      {importOpen ? (
        <Dialog
          title={t("observation.import")}
          onClose={() => setImportOpen(false)}
          busy={importCatalog.isPending}
          actions={
            <>
              <Button
                variant="secondary"
                disabled={importCatalog.isPending}
                onClick={() => setImportOpen(false)}
              >
                {t("common.cancel")}
              </Button>
              <Button
                loading={importCatalog.isPending}
                onClick={() => importCatalog.mutate(undefined)}
              >
                {t("observation.importRun")}
              </Button>
            </>
          }
        >
          <p>{t("observation.importConfirm", { url: CATALOG_URL })}</p>
          <p className="field-hint">{t("observation.importHint")}</p>
          {importCatalog.error ? <ErrorState error={importCatalog.error} /> : null}
        </Dialog>
      ) : null}
      {billing ? <BillingRuleDialog {...billing} onClose={() => setBilling(null)} /> : null}
      {pruneOpen ? (
        <ConfirmDialog
          title={t("modelsPage.siteProbe.prune")}
          message={t("modelsPage.siteProbe.pruneHint")}
          pending={prune.isPending}
          error={prune.error}
          onConfirm={() => prune.mutate(undefined)}
          onClose={() => setPruneOpen(false)}
        />
      ) : null}
      {sourceSite ? (
        <SourceSettings
          site={sourceSite}
          policy={
            (observations.data?.sites ?? []).find((site) => site.site_id === sourceSite.id)
              ?.policy ?? DEFAULT_POLICY
          }
          autoApply={
            (observations.data?.sites ?? []).find((site) => site.site_id === sourceSite.id)
              ?.auto_apply ?? false
          }
          onClose={() => setSourceSite(null)}
        />
      ) : null}
    </section>
  );
}
function ObservationRow({
  item,
  links,
  onOpenBilling,
}: {
  item: SiteObservation;
  links: import("../../api/types").SiteProbeRow[];
  onOpenBilling: (
    member: import("../../api/types").SiteProbeRow["members"][number] & { route: string },
  ) => void;
}) {
  const { t } = useI18n();
  const linked = new Set(links.map((row) => row.route));
  const members = [
    ...new Map(
      links
        .flatMap((row) => row.members.map((member) => ({ ...member, route: row.route })))
        .map((member) => [member.member_id, member]),
    ).values(),
  ];
  return (
    <div className="observation-model">
      <div className="observation-model-name">
        <code title={item.raw_model}>{item.raw_model}</code>
        {item.group_name ? <small>{item.group_name}</small> : null}
        {item.weak_evidence ? <small>{t("modelsPage.siteProbe.weakEvidence")}</small> : null}
      </div>
      <AvailabilityIndicator
        item={{
          model: item.raw_model,
          site_id: item.site_id,
          primary: item.series.ratio !== null ? item.series : null,
          evidence: [item.series],
        }}
      />
      <div className="observation-price">
        <span>
          {item.price.unparsed ? (
            // A raw billing expression is not a price an operator can read.
            // Keep it reachable as a tooltip instead of dumping it in the cell.
            <span className="muted" title={item.price.raw ?? ""}>
              {t("observation.expressionPrice")}
            </span>
          ) : (
            formatObservedPrice(item.price)
          )}
          {item.price.unit?.kind === "site_credit" ? ` ${t("billing.credit")}` : ""}
        </span>
        {item.price.mode ? (
          <small>
            {t(item.price.mode === "fixed" ? "observation.perRequest" : "observation.perMillion")}
          </small>
        ) : null}
      </div>
      <div className="observation-model-links">
        {[...linked].map((route) => (
          <Link key={route} to={`/models?model=${encodeURIComponent(route)}`}>
            {route}
          </Link>
        ))}
        {/* One entry point, not one button per channel: the same model served by
            three channels used to stack three "billing rule" buttons and bury
            the row. A single menu names the channel when it asks which one. */}
        {members.length === 1 ? (
          <Button variant="quiet" onClick={() => onOpenBilling(members[0]!)}>
            {t("billing.open")}
          </Button>
        ) : members.length > 1 ? (
          <ActionMenu
            label={`${t("billing.open")} · ${members.length}`}
            title={t("billing.open")}
            items={members.map((member) => ({
              key: String(member.member_id),
              label: member.channel_name,
              onSelect: () => onOpenBilling(member),
            }))}
          />
        ) : null}
      </div>
    </div>
  );
}
function PolicyFields({
  value,
  onChange,
  disabled,
}: {
  value: SiteProbePolicy;
  onChange: (value: SiteProbePolicy) => void;
  disabled: boolean;
}) {
  const { t } = useI18n();
  return (
    <div className="observation-policy-fields">
      {(
        [
          ["ratio_threshold", "threshold", 100],
          ["min_samples", "minSamples", 1000],
          ["low_rounds", "lowRounds", 10],
          ["high_rounds", "highRounds", 10],
        ] as const
      ).map(([key, label, max]) => (
        <label className="field" key={key}>
          <span>{t(`modelsPage.siteProbe.${label}`)}</span>
          <input
            type="number"
            min="1"
            max={max}
            disabled={disabled}
            value={key === "ratio_threshold" ? Math.round(value[key] * 100) : value[key]}
            onChange={(event) =>
              onChange({
                ...value,
                [key]: Number(event.target.value) / (key === "ratio_threshold" ? 100 : 1),
              })
            }
          />
        </label>
      ))}
    </div>
  );
}
function SourceSettings({
  site,
  policy: initialPolicy,
  autoApply,
  onClose,
}: {
  site: Site;
  policy: SiteProbePolicy;
  autoApply: boolean;
  onClose: () => void;
}) {
  const { client } = useSession();
  const { t } = useI18n();
  const service = api(client!);
  const [automatic, setAutomatic] = useState(site.probe_auto ?? true);
  const [url, setURL] = useState(site.probe_source_url ?? "");
  const [policy, setPolicy] = useState(initialPolicy);
  const [kind, setKind] = useState(site.probe_source_kind ?? "");
  const draftRef = useRef({ url, automatic });
  draftRef.current = { url, automatic };
  const detect = useAdminMutation({
    mutationFn: (value: string) => service.siteProbeDetect(value),
    onSuccess: (data, value) => {
      if (value === draftRef.current.url.trim() && !draftRef.current.automatic) {
        setKind(data.kind);
        setURL(data.url);
      }
    },
  });
  const save = useAdminMutation({
    mutationFn: () =>
      service.saveSiteProbeSource({
        site_id: site.id,
        auto: automatic,
        kind: automatic ? "" : kind,
        url: automatic ? "" : url,
        enabled: site.probe_source_enabled,
        config: JSON.stringify({ auto_apply: autoApply, policy }),
      }),
    invalidateKeys: INVALIDATE,
    onSuccess: onClose,
  });
  const clear = useAdminMutation({
    mutationFn: () => service.clearSiteProbeSource(site.id),
    invalidateKeys: INVALIDATE,
    onSuccess: onClose,
  });
  const busy = detect.isPending || save.isPending || clear.isPending;
  return (
    <Dialog
      title={`${t("observation.source")} · ${site.name}`}
      onClose={onClose}
      busy={busy}
      actions={
        <>
          <Button variant="quiet" onClick={() => clear.mutate(undefined)} disabled={busy}>
            {t("modelsPage.siteProbe.clear")}
          </Button>
          <span className="bar-spacer" />
          <Button variant="secondary" onClick={onClose} disabled={busy}>
            {t("common.cancel")}
          </Button>
          <Button
            disabled={busy || !validObservationPolicy(policy) || (!automatic && (!kind || !url))}
            onClick={() => save.mutate(undefined)}
          >
            {t("modelsPage.siteProbe.save")}
          </Button>
        </>
      }
    >
      <label className="check">
        <input
          type="checkbox"
          checked={automatic}
          disabled={busy}
          onChange={(event) => setAutomatic(event.target.checked)}
        />
        <span>{t("modelsPage.siteProbe.autoSource")}</span>
        <InfoTip label={t("modelsPage.siteProbe.autoSourceHint")} />
      </label>
      {!automatic ? (
        <div className="observation-source-url">
          <label className="field">
            <span>{t("modelsPage.siteProbe.sourceUrl")}</span>
            <input
              value={url}
              disabled={save.isPending || clear.isPending}
              aria-label={t("modelsPage.siteProbe.sourceUrl")}
              onChange={(event) => {
                setURL(event.target.value);
                setKind("");
              }}
            />
          </label>
          <Button
            variant="secondary"
            disabled={!url.trim() || busy}
            onClick={() => detect.mutate(url.trim())}
          >
            {t("modelsPage.siteProbe.detect")}
          </Button>
        </div>
      ) : null}
      <PolicyFields value={policy} onChange={setPolicy} disabled={busy} />
      {detect.data ? (
        <p className="field-hint">
          {t(`observation.kind.${detect.data.kind}`)} · {detect.data.title || detect.data.url}
        </p>
      ) : null}
      {detect.error || save.error || clear.error ? (
        <ErrorState error={detect.error || save.error || clear.error} />
      ) : null}
    </Dialog>
  );
}
