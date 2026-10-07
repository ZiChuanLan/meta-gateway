import { ModelFamilyFilters } from "../features/models/ModelFamilyFilters";
import { ModelWorkspaceLayout } from "../features/models/ModelWorkspaceLayout";
import { ModelPriceSummary } from "../features/models/ModelPriceSummary";
import { ModelFacts } from "../features/models/ModelFacts";
import { ModelDirectoryToolbar } from "../components/ModelDirectoryToolbar";
import { useI18n } from "../i18n";
import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ArrowUpRight, Boxes, RefreshCw, SlidersHorizontal } from "lucide-react";
import { Button, Page, Panel, Loading, ErrorState } from "../components/ui";
import { NoticeBar } from "../components/NoticeBar";
import { EmptyHero } from "../components/EmptyHero";
import { ListShell } from "../components/ListShell";
import { PaginationBar } from "../components/PaginationBar";
import { TelemetryStrip } from "../components/TelemetryStrip";
import { useClientPagination } from "../hooks/useClientPagination";
import { autoModelGroup } from "../lib/modelGroups";
import { ModelCard, ModelCardGrid } from "../features/models/ModelCard";
import { accountRequest } from "../team/transport";
import { teamText } from "../team/text";
import type { Plan, RouteList, UserModel } from "../team/types";
import { RouteOrderDialog } from "./RouteOrderDialog";

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
  const [search, setSearch] = useState("");
  const [group, setGroup] = useState("all");
  const [arranging, setArranging] = useState("");
  const [selection, setSelection] = useState("");

  const items = useMemo(() => query.data ?? [], [query.data]);
  const arrangedModels = useMemo(() => new Set(arranged.data?.models ?? []), [arranged.data]);
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
  const pagination = useClientPagination(filtered);
  const selected = filtered.find((model) => model.name === selection) ?? filtered[0];

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
          {
            label: t("authorizedCandidates"),
            value: items.reduce((sum, model) => sum + model.candidates, 0),
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
              {canRoute ? (
                <label className="models-plan-picker">
                  <span className="workspace-caption">{t("planScope")}</span>
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
                  {pagination.pageItems.map((model) => (
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
                  ))}
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
