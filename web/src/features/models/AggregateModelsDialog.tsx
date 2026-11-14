import { useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Check, Layers, Trash2, X } from "lucide-react";
import { api } from "../../api/client";
import type { ModelAggregate } from "../../api/types";
import { Button, Dialog, Empty } from "../../components/ui";
import { useAdminMutation } from "../../hooks/useAdminMutation";
import { useI18n } from "../../i18n";
import { useSession } from "../../session";

const AGGREGATE_INVALIDATE_KEYS = [
  ["routes"],
  ["route-overviews"],
  ["members"],
  ["models"],
  ["missing-models"],
  ["model-aggregates"],
] as const;

/**
 * The aggregate-model tool: create one public model name that fans out to
 * several existing models (each member keeps forwarding its real upstream
 * name), manage the aggregates already created.
 *
 * The create form picks models from the routes the gateway already serves;
 * per pick the operator may narrow which channels are taken (empty = every
 * enabled channel serving that model). The server resolves the picks into
 * members with explicit {"real": …} redirects at create time.
 */
export function AggregateModelsDialog({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const { client } = useSession();
  const service = api(client!);
  const [creating, setCreating] = useState(false);
  const [name, setName] = useState("");
  const [note, setNote] = useState("");
  /** model name → source channel ids ([] = every channel serving it). */
  const [picked, setPicked] = useState<Record<string, number[]>>({});
  const [expanded, setExpanded] = useState<string | null>(null);
  const [result, setResult] = useState<{ created: number } | null>(null);
  const [confirmRemove, setConfirmRemove] = useState<ModelAggregate | null>(null);

  const aggregates = useQuery({
    queryKey: ["model-aggregates"],
    queryFn: ({ signal }) => service.modelAggregates(signal),
  });

  // The route catalogue is the pick list: every route is a name clients can
  // already call, so every route is a legal aggregation source.
  const overviews = useQuery({
    queryKey: ["route-overviews"],
    queryFn: ({ signal }) => service.routeOverviews(signal),
  });

  const channels = useQuery({
    queryKey: ["channels"],
    queryFn: ({ signal }) => service.channels(signal),
  });

  const routeNames = useMemo(() => {
    const set = new Set<string>();
    for (const item of overviews.data ?? []) {
      if (item.route.enabled) set.add(item.route.model_pattern);
    }
    return [...set].sort((a, b) => a.localeCompare(b));
  }, [overviews.data]);

  const create = useAdminMutation({
    mutationFn: (input: {
      name: string;
      note?: string;
      members: { model: string; source_channel_ids?: number[] }[];
    }) => service.createModelAggregate(input),
    invalidateKeys: [...AGGREGATE_INVALIDATE_KEYS],
    onSuccess: () => {
      setResult({ created: 1 });
      setName("");
      setNote("");
      setPicked({});
      setCreating(false);
    },
  });

  const remove = useAdminMutation({
    mutationFn: (input: { id: number; withRoute: boolean }) =>
      service.deleteModelAggregate(input.id, input.withRoute),
    invalidateKeys: [...AGGREGATE_INVALIDATE_KEYS],
    onSuccess: () => setConfirmRemove(null),
  });

  const pickedNames = Object.keys(picked).filter((model) => picked[model] !== undefined);
  const canSubmit = name.trim().length > 0 && pickedNames.length > 0 && !create.isPending;

  const submit = () => {
    if (!canSubmit) return;
    create.mutate({
      name: name.trim(),
      note: note.trim() || undefined,
      members: pickedNames.map((model) => ({
        model,
        source_channel_ids: (picked[model] ?? []).length > 0 ? picked[model] : undefined,
      })),
    });
  };

  return (
    <Dialog
      title={t("modelsPage.aggregates.title")}
      onClose={onClose}
      busy={create.isPending || remove.isPending}
      actions={
        creating ? (
          <>
            <Button variant="quiet" onClick={() => setCreating(false)}>
              {t("common.cancel")}
            </Button>
            <Button variant="primary" disabled={!canSubmit} onClick={submit}>
              {t("modelsPage.aggregates.create")}
            </Button>
          </>
        ) : (
          <>
            <Button variant="quiet" onClick={onClose}>
              {t("common.close")}
            </Button>
            <Button variant="primary" icon={<Layers size={14} />} onClick={() => setCreating(true)}>
              {t("modelsPage.aggregates.create")}
            </Button>
          </>
        )
      }
    >
      {result ? (
        <div className="unify-result" role="status">
          <Check size={14} />
          {t("modelsPage.aggregates.created")}
        </div>
      ) : null}

      {creating ? (
        <>
          <p className="unify-intro">{t("modelsPage.aggregates.createIntro")}</p>
          <label className="field">
            <span>{t("modelsPage.aggregates.name")}</span>
            <input
              value={name}
              placeholder={t("modelsPage.aggregates.namePlaceholder")}
              onChange={(event) => setName(event.target.value)}
            />
          </label>
          <label className="field">
            <span>{t("modelsPage.aggregates.note")}</span>
            <input value={note} onChange={(event) => setNote(event.target.value)} />
          </label>
          <section className="unify-section">
            <h3>{t("modelsPage.aggregates.pickTitle")}</h3>
            <p className="unify-section-hint">{t("modelsPage.aggregates.pickHint")}</p>
            {overviews.isPending ? (
              <Empty>{t("common.loading")}</Empty>
            ) : routeNames.length === 0 ? (
              <Empty>{t("modelsPage.aggregates.pickEmpty")}</Empty>
            ) : (
              <div className="aggregate-picks">
                {routeNames.map((model) => {
                  const checked = picked[model] !== undefined;
                  const isOpen = expanded === model;
                  return (
                    <div key={model} className="aggregate-pick">
                      <label className="check">
                        <input
                          type="checkbox"
                          checked={checked}
                          onChange={(event) =>
                            setPicked((current) => {
                              const next = { ...current };
                              if (event.target.checked) next[model] = [];
                              else delete next[model];
                              return next;
                            })
                          }
                        />
                        <span className="mono">{model}</span>
                      </label>
                      {checked ? (
                        <button
                          type="button"
                          className="unify-covered-toggle"
                          onClick={() => setExpanded(isOpen ? null : model)}
                        >
                          {(picked[model] ?? []).length > 0
                            ? t("modelsPage.aggregates.channelCount", {
                                n: (picked[model] ?? []).length,
                              })
                            : t("modelsPage.aggregates.allChannels")}
                        </button>
                      ) : null}
                      {checked && isOpen && channels.data ? (
                        <div className="aggregate-channels">
                          {(channels.data ?? [])
                            .filter((channel) => channel.status === "enabled")
                            .map((channel) => (
                              <label className="check" key={channel.id}>
                                <input
                                  type="checkbox"
                                  checked={(picked[model] ?? []).includes(channel.id)}
                                  onChange={(event) =>
                                    setPicked((current) => {
                                      const list = current[model] ?? [];
                                      const next = event.target.checked
                                        ? [...list, channel.id]
                                        : list.filter((id) => id !== channel.id);
                                      return { ...current, [model]: next };
                                    })
                                  }
                                />
                                <span>{channel.name}</span>
                              </label>
                            ))}
                        </div>
                      ) : null}
                    </div>
                  );
                })}
              </div>
            )}
          </section>
        </>
      ) : (
        <>
          <p className="unify-intro">{t("modelsPage.aggregates.intro")}</p>
          {aggregates.isPending ? (
            <Empty>{t("common.loading")}</Empty>
          ) : (aggregates.data?.items.length ?? 0) === 0 ? (
            <Empty>{t("modelsPage.aggregates.empty")}</Empty>
          ) : (
            <div className="aggregate-list">
              {(aggregates.data?.items ?? []).map((agg) => (
                <div key={agg.id} className="aggregate-row">
                  <div className="aggregate-name">
                    <span className="mono">{agg.name}</span>
                    {!agg.route_alive ? (
                      <span className="capability-chip is-warn">
                        {t("modelsPage.aggregates.routeGone")}
                      </span>
                    ) : null}
                  </div>
                  {agg.note ? <span className="muted">{agg.note}</span> : null}
                  <button
                    type="button"
                    className="unify-covered-toggle"
                    onClick={() => setConfirmRemove(agg)}
                  >
                    <Trash2 size={12} /> {t("common.delete")}
                  </button>
                </div>
              ))}
            </div>
          )}
        </>
      )}

      {confirmRemove ? (
        <Dialog
          title={t("modelsPage.aggregates.removeTitle", { name: confirmRemove.name })}
          onClose={() => setConfirmRemove(null)}
          danger
          busy={remove.isPending}
          actions={
            <>
              <Button variant="quiet" onClick={() => setConfirmRemove(null)}>
                {t("common.cancel")}
              </Button>
              <Button
                variant="secondary"
                disabled={remove.isPending}
                onClick={() => {
                  remove.mutate({ id: confirmRemove.id, withRoute: false });
                }}
              >
                {t("modelsPage.aggregates.removeKeepRoute")}
              </Button>
              <Button
                variant="danger"
                disabled={remove.isPending}
                onClick={() => {
                  remove.mutate({ id: confirmRemove.id, withRoute: true });
                }}
              >
                {t("modelsPage.aggregates.removeWithRoute")}
              </Button>
            </>
          }
        >
          <p>{t("modelsPage.aggregates.removeBody")}</p>
          <button
            type="button"
            className="unify-covered-toggle"
            onClick={() => setConfirmRemove(null)}
          >
            <X size={12} /> {t("common.close")}
          </button>
        </Dialog>
      ) : null}
    </Dialog>
  );
}
