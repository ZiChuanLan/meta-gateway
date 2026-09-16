import { useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Image as ImageIcon, MessageSquare, SlidersHorizontal } from "lucide-react";
import { api } from "../api/client";
import type { CatalogFieldChange, CatalogPreviewItem, ModelCapability } from "../api/types";
import {
  Button,
  Dialog,
  Empty,
  ErrorState,
  Field,
  Page,
  Panel,
  Tabs,
  formatDate,
} from "../components/ui";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import ImageStudio from "./workbench/ImageStudio";
import Playground from "./workbench/Playground";

type TabValue = "images" | "text" | "capabilities";

export default function Workbench() {
  const { t } = useI18n();
  const [tab, setTab] = useState<TabValue>("images");
  return (
    <Page title={t("workbench.title")} description={t("workbench.desc")}>
      <Tabs
        items={[
          { value: "images", label: t("workbench.tabImages"), icon: <ImageIcon size={13} /> },
          { value: "text", label: t("workbench.tabText"), icon: <MessageSquare size={13} /> },
          {
            value: "capabilities",
            label: t("workbench.tabCapabilities"),
            icon: <SlidersHorizontal size={13} />,
          },
        ]}
        active={tab}
        onChange={(value) => setTab(value as TabValue)}
      />
      <div hidden={tab !== "images"}><ImageStudio active={tab === "images"} /></div>
      <div hidden={tab !== "text"}><Playground active={tab === "text"} /></div>
      {tab === "capabilities" ? <CapabilityRegistry /> : null}
    </Page>
  );
}

const KIND_KEYS: Record<string, string> = {
  chat: "workbench.cap.kindChat",
  image_gen: "workbench.cap.kindImageGen",
  image_edit: "workbench.cap.kindImageEdit",
  video: "workbench.cap.kindVideo",
  embedding: "workbench.cap.kindEmbedding",
  audio_tts: "workbench.cap.kindTTS",
  audio_stt: "workbench.cap.kindSTT",
  rerank: "workbench.cap.kindRerank",
  moderation: "workbench.cap.kindModeration",
};

function kindLabel(t: (key: string) => string, kind: string) {
  const key = KIND_KEYS[kind];
  return key ? t(key) : kind;
}

function CapabilityRegistry() {
  const { t } = useI18n();
  const { client } = useSession();
  const service = api(client!);
  const queryClient = useQueryClient();
  const invalidateCapabilities = () => Promise.all([
    queryClient.invalidateQueries({ queryKey: ["model-capabilities"] }),
    queryClient.invalidateQueries({ queryKey: ["capabilities"] }),
  ]);
  const [editing, setEditing] = useState<ModelCapability | null>(null);
  const [newModel, setNewModel] = useState("");

  const list = useQuery({
    queryKey: ["model-capabilities"],
    queryFn: ({ signal }) => service.modelCapabilities(signal),
  });

  const save = useMutation({
    mutationFn: (value: ModelCapability) =>
      service.upsertModelCapability(value.model, value),
    onSuccess: async () => {
      setEditing(null);
      setNewModel("");
      await invalidateCapabilities();
    },
  });

  const remove = useMutation({
    mutationFn: (model: string) => service.deleteModelCapability(model),
    onSuccess: invalidateCapabilities,
  });

  const routes = useQuery({
    queryKey: ["route-overviews"],
    queryFn: ({ signal }) => service.routeOverviews(signal),
  });

  const autoTag = useMutation({
    mutationFn: () =>
      service.autoTagModelCapabilities(
        Array.from(new Set((routes.data ?? []).map((item) => item.route.model_pattern))),
      ),
    onSuccess: invalidateCapabilities,
  });

  const status = useQuery({
    queryKey: ["model-capabilities", "catalog"],
    queryFn: ({ signal }) => service.catalogStatus(signal),
  });
  const [catalogOpen, setCatalogOpen] = useState(false);

  const items = list.data?.items ?? [];
  const lastSync = status.data?.state ?? null;
  // A console can outrun the server it talks to (the image is updated
  // separately), so treat a missing field as "this gateway has no catalogs"
  // rather than reading a property off undefined.
  const catalogSources = status.data?.sources ?? [];

  return (
    <Panel
      title={t("workbench.cap.title")}
      titleHelp={t("workbench.cap.help")}
      actions={
        <>
          <input
            value={newModel}
            placeholder={t("workbench.cap.addPlaceholder")}
            onChange={(e) => setNewModel(e.target.value)}
          />
          <Button
            variant="secondary"
            disabled={!newModel.trim() || save.isPending}
            onClick={() => {
              const name = newModel.trim();
              const generated: ModelCapability = {
                model: name,
                kind: "chat",
                provider: "",
                endpoints: ["/v1/chat/completions"],
                input_formats: ["json"],
                input_modalities: ["text"],
                output_modalities: ["text"],
                max_input_images: 0,
                supports_stream: true,
                supports_tools: false,
                supports_json_mode: false,
                async_task: false,
                size_options: "",
                source: "manual",
                notes: "",
              };
              save.mutate(generated);
            }}
          >
            {t("workbench.cap.add")}
          </Button>
          <Button
            variant="secondary"
            disabled={autoTag.isPending}
            onClick={() => autoTag.mutate()}
          >
            {t("workbench.cap.autoTag")}
          </Button>
          <Button
            disabled={catalogSources.length === 0}
            onClick={() => setCatalogOpen(true)}
          >
            {t("workbench.cap.catalog.sync")}
          </Button>
        </>
      }
    >
      <p className="panel-hint">{t("workbench.cap.desc")}</p>
      {catalogSources.length > 0 ? (
        <p className="catalog-status muted">
          {lastSync
            ? t("workbench.cap.catalog.lastSync", {
                when: formatDate(lastSync.synced_at),
                matched: lastSync.matched,
                capabilities: lastSync.capabilities,
                metadata: lastSync.metadata,
                prices: lastSync.prices,
              })
            : t("workbench.cap.catalog.neverSynced")}
          {" · "}
          {status.data?.scheduled
            ? t("workbench.cap.catalog.scheduled")
            : t("workbench.cap.catalog.manualOnly")}
          {" · "}
          {status.data?.prices_enabled
            ? t("workbench.cap.catalog.pricesOn")
            : t("workbench.cap.catalog.pricesOff")}
          {" · "}
          <span className="mono">{catalogSources.join(" + ")}</span>
        </p>
      ) : null}
      {list.isLoading ? <p className="muted">{t("common.working")}</p> : null}
      {list.isError ? <ErrorState error={list.error} /> : null}
      {save.isError && !editing ? <ErrorState error={save.error} /> : null}
      {remove.isError ? <ErrorState error={remove.error} /> : null}
      {autoTag.isError ? <ErrorState error={autoTag.error} /> : null}
      {!list.isLoading && !list.isError && items.length === 0 ? (
        <Empty>{t("workbench.cap.empty")}</Empty>
      ) : null}
      {items.length > 0 ? (
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>{t("workbench.cap.model")}</th>
                <th>{t("workbench.cap.kind")}</th>
                <th>{t("workbench.cap.endpoints")}</th>
                <th>{t("workbench.cap.formats")}</th>
                <th>{t("workbench.cap.maxImages")}</th>
                <th>{t("workbench.cap.source")}</th>
                <th className="actions">{t("common.actions")}</th>
              </tr>
            </thead>
            <tbody>
              {items.map((item) => (
                <tr key={item.model}>
                  <td className="mono">{item.model}</td>
                  <td>{kindLabel(t, item.kind)}</td>
                  <td className="mono">{item.endpoints.join(", ")}</td>
                  <td className="mono">{item.input_formats.join(", ")}</td>
                  <td>{item.max_input_images || "—"}</td>
                  <td>{t(`workbench.cap.source.${item.source}`)}</td>
                  <td className="actions row-actions">
                    <Button variant="secondary" onClick={() => setEditing(item)}>
                      {t("common.edit")}
                    </Button>
                    <Button
                      variant="danger"
                      disabled={remove.isPending}
                      onClick={() => remove.mutate(item.model)}
                    >
                      {t("common.delete")}
                    </Button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}

      {editing ? (
        <CapabilityDialog
          value={editing}
          pending={save.isPending}
          error={save.error}
          onClose={() => setEditing(null)}
          onSave={(value) => save.mutate(value)}
        />
      ) : null}

      {catalogOpen ? (
        <CatalogDialog
          onClose={() => setCatalogOpen(false)}
          onSynced={async () => {
            setCatalogOpen(false);
            await invalidateCapabilities();
            await queryClient.invalidateQueries({
              queryKey: ["model-capabilities", "catalog"],
            });
            // A sync can fill prices and vendor metadata, which the Models page
            // renders from its own query.
            await queryClient.invalidateQueries({ queryKey: ["model-metadata"] });
          }}
        />
      ) : null}
    </Panel>
  );
}

/** Localized labels for the field names a preview reports. Unknown fields fall
 *  through to their raw name so a backend addition stays visible. */
const CATALOG_FIELD_KEYS: Record<string, string> = {
  kind: "workbench.cap.kind",
  provider: "workbench.cap.provider",
  endpoints: "workbench.cap.endpoints",
  input_formats: "workbench.cap.formats",
  input_modalities: "workbench.cap.inputs",
  output_modalities: "workbench.cap.outputs",
  max_input_images: "workbench.cap.maxImages",
  supports_stream: "workbench.cap.fieldStream",
  supports_tools: "workbench.cap.fieldTools",
  supports_json_mode: "workbench.cap.fieldJsonMode",
  context_window: "workbench.cap.fieldContext",
  supports_thinking: "workbench.cap.fieldThinking",
  vendor: "workbench.cap.fieldVendor",
  price_prompt_per_1k: "workbench.cap.fieldPricePrompt",
  price_completion_per_1k: "workbench.cap.fieldPriceCompletion",
  price_cache_per_1k: "workbench.cap.fieldPriceCache",
};

/**
 * The catalog sync dialog. It opens on a dry run rather than a sync button: the
 * whole point of the plan is that an operator can see every field a write would
 * touch — and, crucially, which models it would leave alone — before committing.
 */
function CatalogDialog({
  onClose,
  onSynced,
}: {
  onClose: () => void;
  onSynced: () => void | Promise<void>;
}) {
  const { t } = useI18n();
  const { client } = useSession();
  const service = api(client!);
  const [writeCapabilities, setWriteCapabilities] = useState(true);
  const [writeMetadata, setWriteMetadata] = useState(true);
  const [writePrices, setWritePrices] = useState(true);
  const [onlyChanges, setOnlyChanges] = useState(true);

  const policy = {
    capabilities: writeCapabilities,
    metadata: writeMetadata,
    prices: writePrices,
  };
  const preview = useQuery({
    queryKey: ["model-capabilities", "catalog", "preview", policy],
    queryFn: () => service.previewCatalog(policy),
  });
  const apply = useMutation({
    mutationFn: () => service.syncCatalog(policy),
    onSuccess: () => void onSynced(),
  });

  const items = preview.data?.items ?? [];
  const changed = items.filter((item) => isChanged(item));
  const visible = onlyChanges ? changed : items;

  return (
    <Dialog title={t("workbench.cap.catalog.title")} onClose={onClose}>
      <p className="panel-hint">{t("workbench.cap.catalog.desc")}</p>
      {preview.isPending ? <p className="muted">{t("common.working")}</p> : null}
      {preview.isError ? <ErrorState error={preview.error} /> : null}
      {preview.data ? (
        <>
          <div className="catalog-toggles">
            <label className="check marginless">
              <input
                type="checkbox"
                checked={writeCapabilities}
                onChange={(event) => setWriteCapabilities(event.target.checked)}
              />
              <span>{t("workbench.cap.catalog.writeCapabilities")}</span>
            </label>
            <label className="check marginless">
              <input
                type="checkbox"
                checked={writeMetadata}
                onChange={(event) => setWriteMetadata(event.target.checked)}
              />
              <span>{t("workbench.cap.catalog.writeMetadata")}</span>
            </label>
            <label className="check marginless">
              <input
                type="checkbox"
                checked={writePrices}
                disabled={!writeMetadata}
                onChange={(event) => setWritePrices(event.target.checked)}
              />
              <span>{t("workbench.cap.catalog.writePrices")}</span>
            </label>
            <label className="check marginless">
              <input
                type="checkbox"
                checked={onlyChanges}
                onChange={(event) => setOnlyChanges(event.target.checked)}
              />
              <span>{t("workbench.cap.catalog.onlyChanges")}</span>
            </label>
          </div>
          <p className="catalog-status muted">
            {t("workbench.cap.catalog.summary", {
              matched: preview.data.matched,
              requested: preview.data.requested,
              missing: preview.data.missing,
              changes: changed.length,
            })}
          </p>
          {preview.data.errors?.length ? (
            <p className="inline-error">
              {t("workbench.cap.catalog.sourceErrors", {
                errors: preview.data.errors.join("; "),
              })}
            </p>
          ) : null}
          {visible.length === 0 ? (
            <Empty>{t("workbench.cap.catalog.nothingToDo")}</Empty>
          ) : (
            <div className="catalog-plan">
              {visible.map((item) => (
                <CatalogPlanRow key={item.model} item={item} />
              ))}
            </div>
          )}
        </>
      ) : null}
      {apply.isError ? <ErrorState error={apply.error} /> : null}
      <div className="dialog-actions">
        <span className="flex-spacer" />
        <Button variant="secondary" disabled={apply.isPending} onClick={onClose}>
          {t("common.cancel")}
        </Button>
        <Button
          disabled={apply.isPending || !preview.data || changed.length === 0}
          onClick={() => apply.mutate()}
        >
          {apply.isPending
            ? t("common.working")
            : t("workbench.cap.catalog.apply", { n: changed.length })}
        </Button>
      </div>
    </Dialog>
  );
}

/** Reports whether a plan proposes any write at all. */
function isChanged(item: CatalogPreviewItem) {
  return (
    item.capability_action === "create" ||
    item.capability_action === "refresh" ||
    item.metadata_action === "create" ||
    item.metadata_action === "fill" ||
    item.price_action === "create" ||
    item.price_action === "fill"
  );
}

function CatalogPlanRow({ item }: { item: CatalogPreviewItem }) {
  const { t } = useI18n();
  const groups: Array<{ label: string; action: string; changes?: CatalogFieldChange[] }> = [
    {
      label: t("workbench.cap.catalog.groupCapability"),
      action: item.capability_action,
      changes: item.capability_changes,
    },
    {
      label: t("workbench.cap.catalog.groupMetadata"),
      action: item.metadata_action,
      changes: item.metadata_changes,
    },
    {
      label: t("workbench.cap.catalog.groupPrices"),
      action: item.price_action,
      changes: item.price_changes,
    },
  ];
  return (
    <article className="catalog-plan-row" data-action={isChanged(item) ? "write" : "skip"}>
      <header>
        <strong className="mono">{item.model}</strong>
        {item.found ? (
          <span className="pg-chip mono">{item.sources.join("+")}</span>
        ) : (
          <span className="pg-chip">{t("workbench.cap.catalog.notFound")}</span>
        )}
        {item.capability_action === "skip_manual" ? (
          <span className="pg-chip is-danger">{t("workbench.cap.catalog.manualKept")}</span>
        ) : null}
      </header>
      {groups.map((group) =>
        group.changes?.length ? (
          <div className="catalog-change-group" key={group.label}>
            <span className="catalog-change-label">{group.label}</span>
            <ul>
              {group.changes.map((change) => {
                const labelKey = CATALOG_FIELD_KEYS[change.field];
                return (
                  <li key={change.field}>
                    <span className="mono">{labelKey ? t(labelKey) : change.field}</span>
                    <span className="catalog-from mono">{change.from || "—"}</span>
                    <span className="catalog-arrow">→</span>
                    <span className="catalog-to mono">{change.to || "—"}</span>
                  </li>
                );
              })}
            </ul>
          </div>
        ) : null,
      )}
      {!isChanged(item) ? (
        <span className="muted">{t("workbench.cap.catalog.noChange")}</span>
      ) : null}
    </article>
  );
}

function CapabilityDialog({
  value,
  pending,
  error,
  onClose,
  onSave,
}: {
  value: ModelCapability;
  pending: boolean;
  error?: unknown;
  onClose: () => void;
  onSave: (value: ModelCapability) => void;
}) {
  const { t } = useI18n();
  const [form, setForm] = useState<ModelCapability>(value);
  const patch = (partial: Partial<ModelCapability>) =>
    setForm((current) => ({ ...current, ...partial }));
  const listField = (raw: string) =>
    raw
      .split(",")
      .map((item) => item.trim())
      .filter(Boolean);

  return (
    <Dialog title={t("workbench.cap.editTitle", { name: value.model })} onClose={onClose}>
      <div className="meta-form">
        <Field label={t("workbench.cap.kind")}>
          <select value={form.kind} onChange={(e) => patch({ kind: e.target.value })}>
            {Object.keys(KIND_KEYS).map((kind) => (
              <option key={kind} value={kind}>
                {kindLabel(t, kind)}
              </option>
            ))}
          </select>
        </Field>
        <Field label={t("workbench.cap.provider")}>
          <input
            value={form.provider}
            placeholder="openai, google, xai…"
            onChange={(e) => patch({ provider: e.target.value })}
          />
        </Field>
        <Field label={t("workbench.cap.endpoints")} hint={t("workbench.cap.endpointsHint")}>
          <input
            className="mono"
            value={form.endpoints.join(",")}
            onChange={(e) => patch({ endpoints: listField(e.target.value) })}
          />
        </Field>
        <Field label={t("workbench.cap.formats")} hint={t("workbench.cap.formatsHint")}>
          <input
            className="mono"
            value={form.input_formats.join(",")}
            onChange={(e) => patch({ input_formats: listField(e.target.value) })}
          />
        </Field>
        <Field label={t("workbench.cap.maxImages")}>
          <input
            type="number"
            min={0}
            value={form.max_input_images}
            onChange={(e) =>
              patch({ max_input_images: Math.max(0, Number(e.target.value) || 0) })
            }
          />
        </Field>
        <Field label={t("workbench.cap.inputs")}>
          <input value={form.input_modalities.join(",")} placeholder="text,image" onChange={(event) => patch({ input_modalities: listField(event.target.value) })} />
        </Field>
        <Field label={t("workbench.cap.outputs")}>
          <input value={form.output_modalities.join(",")} placeholder="image" onChange={(event) => patch({ output_modalities: listField(event.target.value) })} />
        </Field>
        <Field label={t("workbench.cap.sizes")}>
          <input value={form.size_options} placeholder="1024x1024,1024x1536" onChange={(event) => patch({ size_options: event.target.value })} />
        </Field>
        <Field label={t("workbench.cap.notes")}>
          <input value={form.notes} onChange={(e) => patch({ notes: e.target.value })} />
        </Field>
      </div>
      {error ? <div className="inline-error">{String(error)}</div> : null}
      <div className="dialog-actions">
        <span className="flex-spacer" />
        <Button variant="secondary" disabled={pending} onClick={onClose}>
          {t("common.cancel")}
        </Button>
        <Button disabled={pending} onClick={() => onSave(form)}>
          {pending ? t("common.working") : t("common.save")}
        </Button>
      </div>
    </Dialog>
  );
}
