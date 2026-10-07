import { ModelFamilyFilters } from "../features/models/ModelFamilyFilters";
import { ModelWorkspaceLayout } from "../features/models/ModelWorkspaceLayout";
import { ModelPriceSummary } from "../features/models/ModelPriceSummary";
import { ModelFacts } from "../features/models/ModelFacts";
import { ModelDirectoryToolbar } from "../components/ModelDirectoryToolbar";
import { useI18n } from "../i18n";
import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowUpRight, Boxes, RefreshCw, SlidersHorizontal, Activity } from "lucide-react";
import { Button, Page, Panel, Loading, ErrorState } from "../components/ui";
import { NoticeBar } from "../components/NoticeBar";
import { EmptyHero } from "../components/EmptyHero";
import { ListShell } from "../components/ListShell";
import { PaginationBar } from "../components/PaginationBar";
import { TelemetryStrip } from "../components/TelemetryStrip";
import { useClientPagination } from "../hooks/useClientPagination";
import { formatCost } from "../lib/format";
import { autoModelGroup } from "../lib/modelGroups";
import { ModelCard, ModelCardGrid } from "../features/models/ModelCard";
import { accountRequest } from "../team/transport";
import { teamText } from "../team/text";
import type {
  MemberModelAvailability,
  MemberModelStat,
  MemberModelStats,
  Plan,
  RouteList,
  UserModel,
} from "../team/types";
import { RouteOrderDialog } from "./RouteOrderDialog";

/** The orderings a member picks between; the default is the catalogue's own. */
type ModelSort = "default" | "usage" | "latency" | "recent";

/**
 * The model catalogue, as this account may use it — and, when the owner allows
 * personal routing, the place where a member arranges each model's upstreams.
 *
 * `/me/model-catalog` already intersects the account's grant with the enabled
 * routes and channels, so this page never filters for authorization; it groups,
 * searches and pages. Arranging edits the member's own order for one model,
 * which is why the badge and the editor live on this list rather than on a
 * separate routing page.
 */
export function ModelsWorkspace({
  userID,
  locale,
  canRoute,
  plans,
  planId,
  onPlanChange,
  onCreatePlan,
  onConnect,
}: {
  userID: number;
  locale: string;
  canRoute: boolean;
  plans: Plan[];
  planId: number;
  onPlanChange: (id: number) => void;
  onCreatePlan: () => void;
  onConnect: (model: string) => void;
}) {
  const t = teamText(locale);
  const { t: ui } = useI18n();
  const query = useQuery({
    queryKey: ["user", userID, "model-catalog"],
    queryFn: ({ signal }) => accountRequest<UserModel[]>("/me/model-catalog", { signal }),
  });
  const arranged = useQuery({
    queryKey: ["user", userID, "routes", planId],
    queryFn: ({ signal }) =>
      accountRequest<RouteList>(`/me/routes${planId ? `?plan_id=${planId}` : ""}`, { signal }),
    enabled: canRoute,
  });
  // What this account has done with each model, and whether the upstreams
  // behind each model are answering. Both are the member's own view of their
  // own page: the figures are their traffic, the health is the gateway's probes
  // for the models they may reach (no upstream is named).
  const stats = useQuery({
    queryKey: ["user", userID, "model-stats"],
    queryFn: ({ signal }) => accountRequest<MemberModelStats>("/me/model-stats", { signal }),
  });
  const health = useQuery({
    queryKey: ["user", userID, "model-availability"],
    queryFn: ({ signal }) =>
      accountRequest<MemberModelAvailability[]>("/me/model-availability", { signal }),
  });
  const [search, setSearch] = useState("");
  const [group, setGroup] = useState("all");
  const [sort, setSort] = useState<ModelSort>("default");
  const [arranging, setArranging] = useState("");
  const [selection, setSelection] = useState("");

  const items = useMemo(() => query.data ?? [], [query.data]);
  const arrangedModels = useMemo(() => new Set(arranged.data?.models ?? []), [arranged.data]);
  const statsByModel = useMemo(
    () =>
      new Map<string, MemberModelStat>((stats.data?.models ?? []).map((row) => [row.model, row])),
    [stats.data],
  );
  const healthByModel = useMemo(
    () =>
      new Map<string, MemberModelAvailability>((health.data ?? []).map((row) => [row.model, row])),
    [health.data],
  );
  const totals = useMemo(() => {
    const rows = stats.data?.models ?? [];
    const ok = rows.reduce((sum, row) => sum + row.ok, 0);
    return {
      requests: rows.reduce((sum, row) => sum + row.requests, 0),
      ok,
      cost: rows.reduce((sum, row) => sum + row.cost, 0),
    };
  }, [stats.data]);
  const groups = useMemo(
    () => [...new Set(items.map((model) => autoModelGroup(model.name, model.vendor)))].sort(),
    [items],
  );
  const filtered = useMemo(
    () =>
      items.filter(
        (model) =>
          (group === "all" || autoModelGroup(model.name, model.vendor) === group) &&
          `${model.name} ${model.vendor} ${model.kind}`
            .toLowerCase()
            .includes(search.toLowerCase()),
      ),
    [items, group, search],
  );
  // Ordering by a figure the account does not have for a model must not move
  // that model to the top with a zero: models with no data rank last.
  const ordered = useMemo(() => {
    if (sort === "default") return filtered;
    const stat = (model: UserModel) => statsByModel.get(model.name);
    return [...filtered].sort((a, b) => {
      const left = stat(a);
      const right = stat(b);
      if (sort === "usage") return (right?.total_tokens ?? -1) - (left?.total_tokens ?? -1);
      if (sort === "latency") {
        if (!left?.requests) return right?.requests ? 1 : a.name.localeCompare(b.name);
        if (!right?.requests) return -1;
        return left.avg_latency_ms - right.avg_latency_ms;
      }
      if (!left?.last_at) return right?.last_at ? 1 : a.name.localeCompare(b.name);
      if (!right?.last_at) return -1;
      return right.last_at.localeCompare(left.last_at);
    });
  }, [filtered, sort, statsByModel]);
  const pagination = useClientPagination(ordered);
  const selected = filtered.find((model) => model.name === selection) ?? filtered[0];
  // The figures are whatever window the endpoint answered with, so the card
  // says that window rather than the one the console happens to default to.
  const windowLabel = useMemo(() => {
    const span = windowSpanHours(stats.data?.since, stats.data?.until);
    if (span === null) return ui("modelsPage.stats.windowRecent");
    if (span === 24) return ui("modelsPage.stats.window24h");
    if (span === 168) return ui("modelsPage.stats.window7d");
    return ui("modelsPage.stats.windowRange");
  }, [stats.data, ui]);

  return (
    <Page
      as="section"
      className="models-page member-model-catalog"
      title={ui("modelsPage.title")}
      description={ui("modelsPage.cardDescription")}
      actions={
        <Button
          variant="secondary"
          icon={<RefreshCw size={15} />}
          onClick={() => {
            void query.refetch();
            if (canRoute) void arranged.refetch();
          }}
          disabled={query.isFetching}
        >
          {t("refresh")}
        </Button>
      }
    >
      <TelemetryStrip
        items={[
          {
            label: t("authorizedModels"),
            value: items.length,
            icon: <Boxes size={16} />,
          },
          { label: t("modelFamilies"), value: groups.length },
          // The account's own traffic, not the gateway's: this is the member's
          // page, and the two numbers that tell them whether yesterday worked
          // are how much they used and what it cost.
          {
            label: ui("modelsPage.stats.windowRequests"),
            value: stats.isPending ? "—" : totals.requests,
            hint:
              totals.requests > 0
                ? ui("modelsPage.stats.windowSuccess", {
                    percent: Math.round((totals.ok / totals.requests) * 100),
                  })
                : undefined,
            icon: <Activity size={16} />,
          },
          {
            label: ui("modelsPage.stats.windowSpend"),
            value: stats.isPending ? "—" : formatCost(totals.cost),
          },
          ...(canRoute ? [{ label: t("arrangedBadge"), value: arrangedModels.size }] : []),
        ]}
      />
      <ModelWorkspaceLayout
        variant="cards"
        directory={
          <Panel className="ops-list-panel model-directory" title={ui("modelsPage.listTitle")}>
            <ModelFamilyFilters
              groups={groups}
              value={group === "all" ? "" : group}
              onChange={(value) => {
                setGroup(value || "all");
                pagination.setPage(1);
              }}
            />
            {/* One line in the deployment's own voice, above the list it applies
                to. The announcement board (operator-authored) renders here too,
                with the same component. */}
            <NoticeBar
              tone="info"
              title={ui("modelsPage.memberTipTitle")}
              body={ui("modelsPage.memberTipBody")}
            />
            <ModelDirectoryToolbar
              value={search}
              label={ui("routing.searchPlaceholder")}
              onChange={(value) => {
                setSearch(value);
                pagination.setPage(1);
              }}
            >
              {/* Search on the left, the two view controls grouped on the right:
                  scattered between them, the row read as three unrelated
                  widgets with a void in the middle. */}
              <div className="models-toolbar-controls">
                <label className="models-sort-picker">
                  <span>{ui("modelsPage.sortLabel")}</span>
                  <select
                    value={sort}
                    onChange={(event) => {
                      setSort(event.target.value as ModelSort);
                      pagination.setPage(1);
                    }}
                  >
                    <option value="default">{ui("modelsPage.sort.default")}</option>
                    <option value="usage">{ui("modelsPage.sort.usage")}</option>
                    <option value="latency">{ui("modelsPage.sort.latency")}</option>
                    <option value="recent">{ui("modelsPage.sort.recent")}</option>
                  </select>
                </label>
                {canRoute ? (
                  <label className="models-plan-picker">
                    <span>{t("planScope")}</span>
                    <select
                      value={planId}
                      onChange={(event) => {
                        const value = Number(event.target.value);
                        if (value === -1) onCreatePlan();
                        else onPlanChange(value);
                      }}
                    >
                      <option value={0}>{t("myDefaultOrder")}</option>
                      {plans
                        .filter((plan) => !plan.default)
                        .map((plan) => (
                          <option key={plan.id} value={plan.id}>
                            {plan.name}
                          </option>
                        ))}
                      <option value={-1}>{t("newPlanOption")}</option>
                    </select>
                  </label>
                ) : null}
              </div>
            </ModelDirectoryToolbar>
            {query.isPending ? (
              <Loading />
            ) : query.error ? (
              <ErrorState error={query.error} retry={() => void query.refetch()} />
            ) : !items.length ? (
              <EmptyHero title={t("noModels")} body={t("modelsWorkspaceHint")} />
            ) : !filtered.length ? (
              <EmptyHero
                title={ui("modelsPage.noMatches")}
                body={ui("modelsPage.noMatchesHint")}
                actions={
                  <Button
                    variant="secondary"
                    onClick={() => {
                      setSearch("");
                      setGroup("all");
                    }}
                  >
                    {ui("modelsPage.clearFilters")}
                  </Button>
                }
              />
            ) : (
              <ListShell
                footer={
                  <PaginationBar
                    {...pagination}
                    onPageChange={pagination.setPage}
                    onPageSizeChange={pagination.setPageSize}
                  />
                }
              >
                {/* The console's own directory table (ModelDirectoryTable): same
              name cell, group + metadata badges, actions column. The member
              view drops the operator columns (upstream, status) and renders
              its own actions per row — the rows are the same object, seen
              without the site's internals. */}
                {/* Cards, not table rows: a row answers "does this model exist", a
                    card has to answer "should I use it" — identity, real specs, the
                    capabilities those specs imply, and the price as this member pays
                    it (group multiplier included). */}
                <ModelCardGrid>
                  {pagination.pageItems.map((model) => {
                    const stat = statsByModel.get(model.name);
                    const probe = healthByModel.get(model.name);
                    return (
                      <ModelCard
                        key={model.name}
                        name={model.name}
                        vendor={model.vendor || model.kind || undefined}
                        group={autoModelGroup(model.name)}
                        arranged={arrangedModels.has(model.name)}
                        capabilities={memberCapabilities(ui, model)}
                        facts={
                          <ModelFacts
                            compact
                            contextWindow={model.context_window}
                            input={model.input_modalities}
                            output={model.output_modalities}
                            thinking={model.supports_thinking}
                          />
                        }
                        stats={
                          stat
                            ? {
                                requests: stat.requests,
                                ok: stat.ok,
                                failed: stat.failed,
                                successRate: stat.requests > 0 ? stat.ok / stat.requests : null,
                                // Only a model this account actually called has a
                                // latency; a stored 0 would read as "instant".
                                avgLatencyMs: stat.requests > 0 ? stat.avg_latency_ms : null,
                                p95Ms: stat.requests > 0 ? stat.p95_ms : null,
                                tokens: stat.total_tokens,
                                cost: stat.cost,
                                windowLabel,
                              }
                            : undefined
                        }
                        health={
                          probe
                            ? {
                                upstreams: probe.upstreams,
                                healthy: probe.healthy,
                                probed: probe.probed,
                                samples: probe.samples,
                                availability: probe.availability,
                                avgLatencyMs: probe.avg_latency_ms,
                              }
                            : undefined
                        }
                        prices={
                          !/[*?]/.test(model.name) ? (
                            <ModelPriceSummary
                              model={model.name}
                              scope="member"
                              request={accountRequest}
                            />
                          ) : null
                        }
                        selected={selected?.name === model.name}
                        onSelect={() => setSelection(model.name)}
                        actions={
                          <>
                            <Button
                              variant="quiet"
                              data-model-details
                              aria-label={ui("modelsPage.viewDetails", { model: model.name })}
                              onClick={() => setSelection(model.name)}
                            >
                              {ui("modelsPage.cardDetailsShort")}
                            </Button>
                            <Button
                              variant="secondary"
                              icon={<ArrowUpRight size={14} />}
                              onClick={() => onConnect(model.name)}
                            >
                              {t("connect")}
                            </Button>
                          </>
                        }
                      />
                    );
                  })}
                </ModelCardGrid>
              </ListShell>
            )}
          </Panel>
        }
        detail={
          selected ? (
            <>
              <div className="detail-head">
                <div>
                  <h2 className="mono">{selected.name}</h2>
                  <small>{selected.vendor || selected.kind || t("unspecified")}</small>
                </div>
              </div>
              <div className="detail-primary-bar">
                <Button icon={<ArrowUpRight size={14} />} onClick={() => onConnect(selected.name)}>
                  {t("connect")}
                </Button>
                {canRoute ? (
                  <Button
                    variant="secondary"
                    icon={<SlidersHorizontal size={14} />}
                    onClick={() => setArranging(selected.name)}
                  >
                    {t("arrange")}
                  </Button>
                ) : null}
              </div>
              <ModelFacts
                contextWindow={selected.context_window}
                input={selected.input_modalities}
                output={selected.output_modalities}
                thinking={selected.supports_thinking}
              />
            </>
          ) : (
            <div className="detail-empty">{ui("modelsPage.selectHint")}</div>
          )
        }
      />
      {arranging ? (
        <RouteOrderDialog
          model={arranging}
          planId={planId}
          locale={locale}
          onClose={() => setArranging("")}
          onSaved={() => void arranged.refetch()}
        />
      ) : null}
    </Page>
  );
}

/**
 * Capabilities derived from the model's own fields, never a claim we cannot
 * source: vision from the declared input modalities, reasoning from the
 * thinking flag, long context from the declared window, image output when the
 * output modality differs from the input.
 */
function memberCapabilities(
  ui: (key: string, vars?: Record<string, string | number>) => string,
  model: UserModel,
): string[] {
  const input = (model.input_modalities || "").toLowerCase();
  const output = (model.output_modalities || "").toLowerCase();
  const caps: string[] = [];
  if (input.includes("image")) caps.push(ui("modelsPage.cap.vision"));
  if ((model.supports_thinking ?? 0) > 0) caps.push(ui("modelsPage.cap.reasoning"));
  if ((model.context_window ?? 0) >= 128000) caps.push(ui("modelsPage.cap.longContext"));
  if (output.includes("image") && !input.includes("image")) {
    caps.push(ui("modelsPage.cap.imageOutput"));
  }
  return caps;
}

/**
 * The span of a stats window, in whole hours, or null when the host gave no
 * bounds. The card labels its figures with this instead of assuming the
 * endpoint's default: a label and a query that can drift apart is how a console
 * starts lying about what it measured.
 */
export function windowSpanHours(since?: string, until?: string): number | null {
  if (!since || !until) return null;
  const from = new Date(since).getTime();
  const to = new Date(until).getTime();
  if (!Number.isFinite(from) || !Number.isFinite(to) || to <= from) return null;
  return Math.round((to - from) / 3_600_000);
}
