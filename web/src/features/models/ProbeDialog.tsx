import { Play, Search, Square } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useMemo, useState } from "react";
import { api } from "../../api/client";
import type { ProbeStartRequest, RouteOverview } from "../../api/types";
import { Button, Dialog, Empty } from "../../components/ui";
import { useAdminMutation } from "../../hooks/useAdminMutation";
import { useI18n } from "../../i18n";
import { useSession } from "../../session";

const PROBE_INVALIDATE_KEYS = [
  ["probe-tasks"],
  ["probe-results"],
  ["model-health"],
] as const;

/**
 * Model probing: send a real completion to every selected (channel, model)
 * pair and report which ones actually answer.
 *
 * Probing costs upstream tokens, so the scope and the cost are always visible
 * before the run starts, the run can be cancelled, and progress is polled
 * rather than blocking the dialog.
 */
export function ProbeDialog({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const { client } = useSession();
  const service = api(client!);
  const [pickedChannels, setPickedChannels] = useState<string[]>([]);
  const [pickedModels, setPickedModels] = useState<string[]>([]);
  const [prompt, setPrompt] = useState("");
  const [maxTokens, setMaxTokens] = useState(1);
  const [concurrency, setConcurrency] = useState(4);
  // 0 leaves routing untouched; above it, this is how many consecutive
  // failures a member survives before the run takes it out of rotation.
  const [autoDisable, setAutoDisable] = useState(0);

  const channels = useQuery({
    queryKey: ["channels"],
    queryFn: ({ signal }) => service.channels(signal),
  });
  const routes = useQuery({
    queryKey: ["route-overviews"],
    queryFn: ({ signal }) => service.routeOverviews(signal),
  });

  // Poll while a run is in flight; the interval turns itself off once idle.
  const tasks = useQuery({
    queryKey: ["probe-tasks"],
    queryFn: () => service.probeTasks(),
    refetchInterval: (query) =>
      query.state.data?.some((task) => task.status === "running") ? 1000 : false,
  });
  const latest = tasks.data?.[0];
  const running = latest?.status === "running";

  const results = useQuery({
    queryKey: ["probe-results", latest?.id],
    queryFn: ({ signal }) => service.probeResults(latest!.id, signal),
    enabled: latest !== undefined,
  });

  const start = useAdminMutation({
    mutationFn: (request: ProbeStartRequest) => service.probeStart(request),
    invalidateKeys: [...PROBE_INVALIDATE_KEYS],
  });
  const cancel = useAdminMutation({
    mutationFn: (id: number) => service.probeCancel(id),
    invalidateKeys: [...PROBE_INVALIDATE_KEYS],
  });

  // The probe targets come from live route members, so the selection space is
  // exactly what the server would walk — a channel with no route member can
  // never be probed and is not worth offering.
  const index = useMemo(() => buildIndex(routes.data ?? []), [routes.data]);
  const channelNames = useMemo(() => {
    const map = new Map<number, string>();
    for (const channel of channels.data ?? []) map.set(channel.id, channel.name);
    return map;
  }, [channels.data]);

  const selectableChannels = useMemo(
    () =>
      (channels.data ?? []).filter((channel) =>
        index.byChannel.has(channel.id),
      ),
    [channels.data, index],
  );

  // Models follow the channel selection: with channels picked, only models
  // those channels actually serve stay in reach.
  const visibleModels = useMemo(() => {
    if (pickedChannels.length === 0) return index.models;
    const union = new Set<string>();
    for (const id of pickedChannels) {
      for (const model of index.byChannel.get(Number(id)) ?? []) union.add(model);
    }
    return index.models.filter((model) => union.has(model));
  }, [pickedChannels, index]);

  const scope = useMemo(() => {
    const ids =
      pickedChannels.length > 0
        ? pickedChannels.map(Number)
        : [...index.byChannel.keys()];
    const models = pickedModels.length > 0 ? pickedModels : visibleModels;
    let pairs = 0;
    for (const id of ids) {
      const owned = index.byChannel.get(id);
      if (!owned) continue;
      for (const model of models) if (owned.has(model)) pairs += 1;
    }
    return {
      channels: ids.length,
      models: models.length,
      pairs,
    };
  }, [pickedChannels, pickedModels, visibleModels, index]);

  const toggleChannel = (id: string, value: boolean) => {
    const next = value
      ? [...pickedChannels, id]
      : pickedChannels.filter((item) => item !== id);
    setPickedChannels(next);
    // A model that no selected channel serves would silently vanish from the
    // run, so drop it here and keep the estimate honest.
    if (next.length > 0) {
      const union = new Set<string>();
      for (const picked of next) {
        for (const model of index.byChannel.get(Number(picked)) ?? []) {
          union.add(model);
        }
      }
      setPickedModels((current) => current.filter((model) => union.has(model)));
    }
  };

  const submit = () => {
    start.mutate({
      channel_ids:
        pickedChannels.length > 0 ? pickedChannels.map(Number) : undefined,
      models: pickedModels.length > 0 ? pickedModels : undefined,
      prompt: prompt.trim() !== "" ? prompt : undefined,
      max_tokens: maxTokens,
      concurrency,
      auto_disable_after: autoDisable > 0 ? autoDisable : undefined,
    });
  };

  return (
    <Dialog
      title={t("modelsPage.probe.title")}
      onClose={onClose}
      actions={
        <>
          <Button variant="secondary" disabled={start.isPending} onClick={onClose}>
            {t("common.close")}
          </Button>
          {running && latest ? (
            <Button
              icon={<Square size={15} />}
              disabled={cancel.isPending}
              onClick={() => cancel.mutate(latest.id)}
            >
              {t("modelsPage.probe.cancel")}
            </Button>
          ) : (
            <Button
              icon={<Play size={15} />}
              disabled={start.isPending || scope.pairs === 0}
              onClick={submit}
            >
              {t("modelsPage.probe.start")}
            </Button>
          )}
        </>
      }
    >
      <p className="unify-intro">{t("modelsPage.probe.description")}</p>

      <div className="probe-picker">
        <PickList
          title={t("modelsPage.probe.channels")}
          searchPlaceholder={t("modelsPage.probe.searchChannels")}
          disabled={running}
          options={selectableChannels.map((channel) => ({
            value: String(channel.id),
            label: channel.name,
            hint: t("modelsPage.probe.modelCount", {
              count: index.byChannel.get(channel.id)?.size ?? 0,
            }),
          }))}
          selected={pickedChannels}
          onChange={setPickedChannels}
          onToggleOne={toggleChannel}
          emptyLabel={t("common.loading")}
        />
        <PickList
          title={t("modelsPage.probe.models")}
          searchPlaceholder={t("modelsPage.probe.searchModels")}
          disabled={running}
          options={visibleModels.map((model) => ({
            value: model,
            label: model,
            hint: t("modelsPage.probe.channelCount", {
              count: index.byModel.get(model)?.size ?? 0,
            }),
          }))}
          selected={pickedModels}
          onChange={setPickedModels}
          emptyLabel={t("common.loading")}
        />
      </div>

      <p className="unify-section-hint">
        {pickedChannels.length > 0
          ? t("modelsPage.probe.filteredHint")
          : t("modelsPage.probe.allHint")}
      </p>

      <label className="field">
        <span>{t("modelsPage.probe.prompt")}</span>
        <textarea
          className="probe-prompt"
          rows={2}
          disabled={running}
          value={prompt}
          placeholder={t("modelsPage.probe.promptPlaceholder")}
          onChange={(event) => setPrompt(event.target.value)}
        />
      </label>

      <div className="field-row">
        <label className="field">
          <span>{t("modelsPage.probe.maxTokens")}</span>
          <input
            type="number"
            min={1}
            max={256}
            disabled={running}
            value={maxTokens}
            onChange={(event) => setMaxTokens(Number(event.target.value))}
          />
        </label>
        <label className="field">
          <span>{t("modelsPage.probe.concurrency")}</span>
          <input
            type="number"
            min={1}
            max={16}
            disabled={running}
            value={concurrency}
            onChange={(event) => setConcurrency(Number(event.target.value))}
          />
        </label>
        <label className="field">
          <span>{t("modelsPage.probe.autoDisable")}</span>
          <input
            type="number"
            min={0}
            max={10}
            disabled={running}
            value={autoDisable}
            onChange={(event) => setAutoDisable(Number(event.target.value))}
          />
        </label>
      </div>

      {/* The estimate sits after the knobs that determine it, so changing the
          scope or the prompt re-reads as a new price before submitting. */}
      <div className="probe-scope" role="status">
        {t("modelsPage.probe.scope", {
          channels: scope.channels,
          models: scope.models,
          pairs: scope.pairs,
        })}
      </div>
      {autoDisable > 0 ? (
        <p className="unify-section-hint">
          {t("modelsPage.probe.autoDisableHint", { count: autoDisable })}
        </p>
      ) : null}

      {start.error ? (
        <div className="inline-error">{String(start.error)}</div>
      ) : null}

      {latest ? (
        <div className="unify-result" role="status">
          {t("modelsPage.probe.progress", {
            done: latest.completed,
            total: latest.total,
            ok: latest.ok_count,
            fail: latest.fail_count,
          })}
          {running ? ` · ${t("modelsPage.probe.running")}` : ""}
        </div>
      ) : null}

      {results.isPending ? (
        <Empty>{t("common.loading")}</Empty>
      ) : (results.data ?? []).length === 0 ? (
        <Empty>{t("modelsPage.probe.noResults")}</Empty>
      ) : (
        <div className="probe-results">
          <table className="table">
            <thead>
              <tr>
                <th>{t("modelsPage.probe.colChannel")}</th>
                <th>{t("modelsPage.probe.colModel")}</th>
                <th>{t("modelsPage.probe.colStatus")}</th>
                <th>{t("modelsPage.probe.colLatency")}</th>
                <th>{t("modelsPage.probe.colError")}</th>
              </tr>
            </thead>
            <tbody>
              {(results.data ?? []).map((result) => (
                <tr key={result.id}>
                  {/* Raw ids are unreadable next to the picker's names. */}
                  <td>{channelNames.get(result.channel_id) ?? result.channel_id}</td>
                  <td className="mono">{result.model}</td>
                  <td>
                    <span
                      className={`model-meta-badge${result.ok ? " is-mapped" : ""}`}
                    >
                      {result.ok
                        ? t("modelsPage.probe.ok")
                        : t("modelsPage.probe.failed")}
                    </span>
                  </td>
                  <td>{result.latency_ms} ms</td>
                  <td className="mono">{result.error ?? ""}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}
    </Dialog>
  );
}

type PickOption = { value: string; label: string; hint: string };

/**
 * One side of the picker: a filterable, scrollable checkbox list with
 * select-all / clear. Used for both channels and models so the two halves of
 * the scope stay visually symmetric.
 */
function PickList({
  title,
  searchPlaceholder,
  options,
  selected,
  onChange,
  onToggleOne,
  disabled,
  emptyLabel,
}: {
  title: string;
  searchPlaceholder: string;
  options: PickOption[];
  selected: string[];
  onChange: (values: string[]) => void;
  onToggleOne?: (value: string, checked: boolean) => void;
  disabled: boolean;
  emptyLabel: string;
}) {
  const { t } = useI18n();
  const [query, setQuery] = useState("");

  const needle = query.trim().toLowerCase();
  const visible = needle
    ? options.filter((option) => option.label.toLowerCase().includes(needle))
    : options;

  const toggle = (value: string, checked: boolean) => {
    if (onToggleOne) {
      onToggleOne(value, checked);
      return;
    }
    onChange(
      checked ? [...selected, value] : selected.filter((item) => item !== value),
    );
  };

  // Select-all applies to what the operator can currently see, so a filter
  // narrows the bulk action instead of silently grabbing everything.
  const allVisibleSelected =
    visible.length > 0 &&
    visible.every((option) => selected.includes(option.value));

  return (
    <section className="probe-picker-col">
      <header className="probe-picker-head">
        <h3>{title}</h3>
        <span className="probe-picker-count">
          {t("modelsPage.probe.selectedCount", { count: selected.length })}
        </span>
      </header>

      <div className="probe-search">
        <Search size={13} />
        <input
          type="search"
          value={query}
          disabled={disabled}
          placeholder={searchPlaceholder}
          onChange={(event) => setQuery(event.target.value)}
        />
      </div>

      <div className="probe-picker-actions">
        <button
          type="button"
          className="unify-covered-toggle"
          disabled={disabled || visible.length === 0}
          onClick={() =>
            onChange(allVisibleSelected
              ? selected.filter(
                  (item) => !visible.some((option) => option.value === item),
                )
              : [
                  ...selected,
                  ...visible
                    .map((option) => option.value)
                    .filter((value) => !selected.includes(value)),
                ])
          }
        >
          {allVisibleSelected
            ? t("modelsPage.probe.clearVisible")
            : t("modelsPage.probe.selectVisible")}
        </button>
        <button
          type="button"
          className="unify-covered-toggle"
          disabled={disabled || selected.length === 0}
          onClick={() => onChange([])}
        >
          {t("modelsPage.probe.clearAll")}
        </button>
      </div>

      <div className="probe-picker-list">
        {visible.length === 0 ? (
          <p className="probe-picker-empty">{emptyLabel}</p>
        ) : (
          visible.map((option) => (
            <label className="check probe-picker-item" key={option.value}>
              <input
                type="checkbox"
                disabled={disabled}
                checked={selected.includes(option.value)}
                onChange={(event) => toggle(option.value, event.target.checked)}
              />
              <span className="probe-picker-label" title={option.label}>
                {option.label}
              </span>
              <span className="probe-picker-hint">{option.hint}</span>
            </label>
          ))
        )}
      </div>
    </section>
  );
}

/**
 * The channel ↔ model graph behind the picker, built from route members the
 * same way the server builds probe targets: only enabled routes and enabled
 * members count.
 */
function buildIndex(overviews: RouteOverview[]) {
  const byChannel = new Map<number, Set<string>>();
  const byModel = new Map<string, Set<number>>();
  for (const overview of overviews) {
    const pattern = overview.route?.model_pattern ?? "";
    if (pattern === "" || !overview.route?.enabled) continue;
    for (const candidate of overview.members ?? []) {
      const member = candidate.member;
      if (!member?.enabled) continue;
      const models = byChannel.get(member.channel_id) ?? new Set<string>();
      models.add(pattern);
      byChannel.set(member.channel_id, models);
      const channels = byModel.get(pattern) ?? new Set<number>();
      channels.add(member.channel_id);
      byModel.set(pattern, channels);
    }
  }
  return { byChannel, byModel, models: [...byModel.keys()].sort() };
}
