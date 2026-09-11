import { ArrowRight, ChevronDown, ChevronUp, RefreshCw } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { useNavigate } from "react-router-dom";
import { api } from "../../api/client";
import type { ModelChange, ModelReplacementPreview, ModelReplacementRequest } from "../../api/types";
import { Button, ConfirmDialog, Dialog, Empty } from "../../components/ui";
import { useAdminMutation } from "../../hooks/useAdminMutation";
import { useI18n } from "../../i18n";
import { useSession } from "../../session";
import "./modelChanges.css";

const INVALIDATE = [["model-changes"], ["route-overviews"], ["routes"], ["members"], ["models"], ["explain"], ["missing-models"], ["channel-overviews"]];
type Filters = { open: boolean; channel: string; kind: string; status: string; query: string };
const INITIAL: Filters = { open: false, channel: "", kind: "", status: "pending", query: "" };
function readFilters(): Filters {
  try { return { ...INITIAL, ...JSON.parse(sessionStorage.getItem("models.changes") || "{}") }; }
  catch { return INITIAL; }
}

// A pending removal without any route member bound to the model cannot break
// routing — candidate-list churn only. These are safe to bulk-ignore.
function harmless(item: ModelChange): boolean {
  return item.kind === "removed" && item.status === "pending" && item.members.length === 0;
}

// Whole days a pending removal has gone unhandled, from first detection.
function missingDays(item: ModelChange): number {
  const detected = Date.parse(item.detected_at);
  if (Number.isNaN(detected)) return 0;
  return Math.floor((Date.now() - detected) / 86_400_000);
}

// Local extension of the model workspace: compact reminder, inline history,
// then a protected selection → server preview → explicit apply workflow.
export function ModelChangesPanel() {
  const { client } = useSession();
  const { t } = useI18n();
  const navigate = useNavigate();
  const service = api(client!);
  const [filters, setFilters] = useState(readFilters);
  const [selected, setSelected] = useState<number[]>([]);
  const [replacement, setReplacement] = useState<ModelChange[] | null>(null);
  const [ignoreIds, setIgnoreIds] = useState<number[] | null>(null);
  const [message, setMessage] = useState("");
  useEffect(() => {
    try { sessionStorage.setItem("models.changes", JSON.stringify(filters)); } catch { /* Storage unavailable. */ }
  }, [filters]);
  const changes = useQuery({ queryKey: ["model-changes"], queryFn: ({ signal }) => service.modelChanges(signal), refetchInterval: 60_000 });
  const ignore = useAdminMutation({
    mutationFn: service.ignoreModelChanges, invalidateKeys: [["model-changes"]], toastOnError: false,
    onSuccess: () => { setIgnoreIds(null); setSelected([]); },
  });
  const items = changes.data?.items ?? [];
  const summary = changes.data?.summary;
  const visible = items.filter(item => (!filters.channel || String(item.channel_id) === filters.channel)
    && (!filters.kind || item.kind === filters.kind) && (!filters.status || item.status === filters.status)
    && `${item.model_name} ${item.channel_name}`.toLowerCase().includes(filters.query.trim().toLowerCase()));
  const chosen = visible.filter(item => selected.includes(item.id) && item.status === "pending");
  const replaceable = chosen.length > 0 && chosen.every(item => item.kind === "removed" && item.members.length > 0 && item.channel_id === chosen[0]!.channel_id);
  const harmlessVisible = visible.filter(harmless);
  const channels = [...new Map(items.map(item => [item.channel_id, item.channel_name])).entries()];
  const setFilter = (patch: Partial<Filters>) => { setFilters(current => ({ ...current, ...patch })); setSelected([]); };
  return <section className="model-changes" aria-label={t("modelChanges.title")}>
    <div className="model-changes-summary">
      <RefreshCw size={15} aria-hidden="true" />
      <strong>{t("modelChanges.title")}</strong>
      {summary && (summary.added > 0 || summary.removed > 0) ? <span>{t("modelChanges.summary", { added: summary.added, removed: summary.removed, routes: summary.affected_routes })}</span> : null}
      {summary && summary.confirmed > 0 ? <span className="model-change-warning">{t("modelChanges.summaryConfirmed", { count: summary.confirmed })}</span> : null}
      <Button variant="secondary" aria-expanded={filters.open} aria-controls="model-change-history" onClick={() => setFilter({ open: !filters.open })} icon={filters.open ? <ChevronUp size={14} /> : <ChevronDown size={14} />}>
        {t(filters.open ? "modelChanges.collapse" : summary && summary.added + summary.removed > 0 ? "modelChanges.open" : "modelChanges.history")}
      </Button>
    </div>
    {message ? <p role="status">{message}</p> : null}
    {changes.isError ? <div className="inline-error" role="alert">{String(changes.error)} <Button variant="secondary" onClick={() => changes.refetch()}>{t("common.retry")}</Button></div> : null}
    {filters.open ? <div id="model-change-history" className="model-change-history">
      <p className="muted">{t("modelChanges.hint")}</p>
      <div className="model-change-filters">
        <input aria-label={t("modelChanges.search")} placeholder={t("modelChanges.search")} value={filters.query} onChange={e => setFilter({ query: e.target.value })} />
        <select aria-label={t("modelChanges.allChannels")} value={filters.channel} onChange={e => setFilter({ channel: e.target.value })}>
          <option value="">{t("modelChanges.allChannels")}</option>{channels.map(([id, name]) => <option value={id} key={id}>{name}</option>)}
        </select>
        <select aria-label={t("modelChanges.kind")} value={filters.kind} onChange={e => setFilter({ kind: e.target.value })}>
          <option value="">{t("modelChanges.allKinds")}</option>{["added", "removed"].map(kind => <option key={kind} value={kind}>{t(`modelChanges.${kind}`)}</option>)}
        </select>
        <select aria-label={t("modelChanges.status")} value={filters.status} onChange={e => setFilter({ status: e.target.value })}>
          <option value="">{t("modelChanges.allStatuses")}</option>{["pending", "ignored", "applied", "resolved"].map(status => <option key={status} value={status}>{t(`modelChanges.${status}`)}</option>)}
        </select>
      </div>
      {chosen.length ? <div className="model-change-bulk">
        <Button disabled={!replaceable} onClick={() => setReplacement(chosen)}>{t("modelChanges.bulk", { count: chosen.length })}</Button>
        <Button variant="secondary" onClick={() => { ignore.reset(); setIgnoreIds(chosen.map(item => item.id)); }}>{t("modelChanges.ignore")}</Button>
        {!replaceable ? <span className="muted">{t("modelChanges.sameChannel")}</span> : null}
      </div> : null}
      {!chosen.length && harmlessVisible.length ? <div className="model-change-bulk">
        <Button
          variant="secondary"
          title={t("modelChanges.ignoreHarmlessHint")}
          onClick={() => { ignore.reset(); setIgnoreIds(harmlessVisible.map(item => item.id)); }}
        >
          {t("modelChanges.ignoreHarmless", { count: harmlessVisible.length })}
        </Button>
      </div> : null}
      {changes.isPending ? <Empty>{t("common.loading")}</Empty> : !visible.length ? <Empty>{t("modelChanges.empty")}</Empty> : <div className="model-change-list">
        {visible.map(item => <article key={item.id} className="model-change-row">
          <div className="model-change-heading">
            {item.status === "pending" ? <input type="checkbox" aria-label={t("modelChanges.select", { model: item.model_name, channel: item.channel_name })} checked={selected.includes(item.id)} onChange={e => setSelected(current => e.target.checked ? [...current, item.id] : current.filter(id => id !== item.id))} /> : null}
            <strong className="mono">{item.model_name}</strong>
            <span className={item.kind === "removed" ? "model-change-warning" : "muted"}>{t(`modelChanges.${item.kind}`)}</span>
            <span className="muted">{t(`modelChanges.${item.status}`)}</span>
          </div>
          <div className="model-change-meta">
            <span>{item.channel_name}</span>
            <time dateTime={item.detected_at}>{new Date(item.detected_at).toLocaleString()}</time>
            {item.status === "pending" && item.kind === "removed" && missingDays(item) >= 1
              ? <span className="muted">{t("modelChanges.missingFor", { days: missingDays(item) })}</span>
              : null}
          </div>
          {item.kind === "removed" && (item.confirmed || item.partial_keys || (item.flap_count ?? 0) > 0 || item.runtime_blocked)
            ? <div className="model-change-signals">
              {item.confirmed ? <span className="model-change-signal is-confirmed" title={t("modelChanges.confirmedHint", { count: item.miss_count ?? 0 })}>{t("modelChanges.confirmedBadge")}</span> : null}
              {item.partial_keys ? <span className="model-change-signal is-partial" title={t("modelChanges.partialKeysHint")}>{t("modelChanges.partialKeys")}</span> : null}
              {(item.flap_count ?? 0) > 0 ? <span className="model-change-signal is-flap">{t("modelChanges.flap", { count: item.flap_count ?? 0 })}</span> : null}
              {item.runtime_blocked ? <span className="model-change-signal is-runtime" title={t("modelChanges.blockedAtHint", { time: item.blocked_at ? new Date(item.blocked_at).toLocaleString() : "—" })}>{t("modelChanges.runtimeBlocked")}</span> : null}
            </div>
            : null}
          {item.kind === "removed" ? <>
            {item.members.length ? <ul className="model-change-impacts">{item.members.map(member => <li key={member.member_id}><strong className="mono">{member.model_pattern}</strong> {member.route_name} <span className="muted">{t("modelChanges.member", { id: member.member_id, group: member.group_name || "default" })}</span></li>)}</ul> : <p className="muted">{t("modelChanges.noImpact")}</p>}
            <p className="muted">{item.candidates.length ? t("modelChanges.candidates") : t("modelChanges.noCandidates")}</p>
            {item.candidates.length ? <div className="model-change-candidates">{item.candidates.map(name => <code key={name}>{name}</code>)}</div> : null}
          </> : null}
          {item.status === "pending" ? <div className="model-change-actions">
            {item.kind === "removed" ? <Button variant="secondary" disabled={!item.members.length} onClick={() => setReplacement([item])}>{t("modelChanges.replace")}</Button> : null}
            {item.kind === "added" ? (
              <Button
                variant="secondary"
                onClick={() => navigate(`/models/channel/${item.channel_id}?model=${encodeURIComponent(item.model_name)}`)}
              >
                {t("modelChanges.adopt")}
              </Button>
            ) : null}
            <Button variant="secondary" onClick={() => { ignore.reset(); setIgnoreIds([item.id]); }}>{t("modelChanges.ignore")}</Button>
          </div> : null}
        </article>)}
      </div>}
    </div> : null}
    {replacement ? <ReplacementDialog changes={replacement} onClose={() => setReplacement(null)} onDone={count => { setReplacement(null); setSelected([]); setMessage(t("modelChanges.done", { count })); }} /> : null}
    {ignoreIds ? <ConfirmDialog title={t("modelChanges.ignore")} confirmLabel={t("modelChanges.ignore")} message={t("modelChanges.ignoreConfirm", { count: ignoreIds.length })} onClose={() => { if (!ignore.isPending) setIgnoreIds(null); }} onConfirm={() => ignore.mutate(ignoreIds)} pending={ignore.isPending} error={ignore.error} /> : null}
  </section>;
}

function ReplacementDialog({ changes, onClose, onDone }: { changes: ModelChange[]; onClose: () => void; onDone: (count: number) => void }) {
  const { client } = useSession();
  const { t } = useI18n();
  const service = api(client!);
  const sourceChannel = changes[0]!.channel_id;
  const members = [...new Map(changes.flatMap(change => change.members).map(member => [member.member_id, member])).values()];
  const [memberIds, setMemberIds] = useState(members.map(member => member.member_id));
  const [crossChannel, setCrossChannel] = useState(false);
  const [targetChannel, setTargetChannel] = useState(sourceChannel);
  const [targetModel, setTargetModel] = useState("");
  const [modelQuery, setModelQuery] = useState("");
  const [preview, setPreview] = useState<{ result: ModelReplacementPreview; request: ModelReplacementRequest } | null>(null);
  const previewHeading = useRef<HTMLHeadingElement>(null);
  const targetSelect = useRef<HTMLSelectElement>(null);
  const previousStep = useRef(false);
  useEffect(() => {
    if (preview) previewHeading.current?.focus();
    else if (previousStep.current) targetSelect.current?.focus();
    previousStep.current = preview !== null;
  }, [preview]);
  const channels = useQuery({ queryKey: ["channels"], queryFn: ({ signal }) => service.channels(signal) });
  const inventory = useQuery({ queryKey: ["replacement-models", targetChannel], queryFn: ({ signal }) => service.discoveredModels(targetChannel, signal), staleTime: 0 });
  const candidates = new Set(targetChannel === sourceChannel ? changes.flatMap(change => change.candidates) : []);
  const models = [...new Set((inventory.data ?? []).filter(model => model.available).map(model => model.model_name))].sort((a, b) => Number(candidates.has(b)) - Number(candidates.has(a)) || a.localeCompare(b));
  const visibleModels = models.filter(model => model.toLowerCase().includes(modelQuery.toLowerCase()) || model === targetModel);
  const prepare = useAdminMutation({ mutationFn: service.previewModelReplacement, toastOnError: false, onSuccess: (result, request) => setPreview({ result, request }) });
  const apply = useAdminMutation({ mutationFn: service.applyModelReplacement, toastOnError: false, invalidateKeys: INVALIDATE, onSuccess: data => onDone(data.updated) });
  const busy = prepare.isPending || apply.isPending;
  const changeTarget = (channel: number) => { setTargetChannel(channel); setTargetModel(""); setModelQuery(""); prepare.reset(); };
  const channelName = (id: number) => channels.data?.find(channel => channel.id === id)?.name ?? (id === sourceChannel ? changes[0]!.channel_name : `#${id}`);
  return <Dialog title={t("modelChanges.replaceTitle")} onClose={() => { if (!busy) onClose(); }}>
    <p className="model-change-preserve">{t("modelChanges.preserve")}</p>
    {preview ? <>
      <h3 ref={previewHeading} tabIndex={-1}>{t("modelChanges.previewTitle")}</h3>
      <div className="model-change-preview">{preview.result.items.map(item => <div key={item.member_id}>
        <strong className="mono">{item.model_pattern}</strong><span className="muted">{item.route_name} · #{item.member_id}</span>
        <div className="model-change-mapping"><span>{channelName(item.source_channel_id)}<code>{item.source_model}</code></span><ArrowRight size={18} aria-hidden="true" /><span>{channelName(item.target_channel_id)}<code>{item.target_model}</code></span></div>
      </div>)}</div>
      {apply.error ? <div role="alert" className="inline-error">{t("modelChanges.stale")} {String(apply.error)}</div> : null}
      <div className="dialog-actions"><Button variant="secondary" disabled={busy} onClick={() => { setPreview(null); prepare.reset(); apply.reset(); }}>{t("modelChanges.back")}</Button><Button disabled={busy || !!apply.error || !preview.result.items.length} onClick={() => apply.mutate({ ...preview.request, preview_token: preview.result.preview_token })}>{busy ? t("common.working") : t("modelChanges.confirm", { count: preview.result.items.length })}</Button></div>
    </> : <>
      <fieldset disabled={busy} className="model-replacement-fields">
        <label className="check"><input type="checkbox" checked={crossChannel} onChange={e => { setCrossChannel(e.target.checked); changeTarget(sourceChannel); }} />{t("modelChanges.cross")}</label>
        {crossChannel ? <p className="model-change-warning">{t("modelChanges.crossWarning")}</p> : null}
        <label>{t("modelChanges.targetChannel")}<select value={targetChannel} disabled={!crossChannel} onChange={e => changeTarget(Number(e.target.value))}>
          <option value={sourceChannel}>{channelName(sourceChannel)}</option>{crossChannel ? (channels.data ?? []).filter(channel => channel.id !== sourceChannel && channel.status === "enabled").map(channel => <option value={channel.id} key={channel.id}>{channel.name}</option>) : null}
        </select></label>
        {channels.isError || inventory.isError ? <div className="inline-error" role="alert">{String(channels.error || inventory.error)}<Button variant="secondary" onClick={() => { void channels.refetch(); void inventory.refetch(); }}>{t("common.retry")}</Button></div> : null}
        <label>{t("modelChanges.search")}<input value={modelQuery} onChange={e => setModelQuery(e.target.value)} /></label>
        <label>{t("modelChanges.targetModel")}<select ref={targetSelect} value={targetModel} disabled={inventory.isPending || inventory.isError} onChange={e => { setTargetModel(e.target.value); prepare.reset(); }}>
          <option value="">{t(inventory.isPending ? "common.loading" : "modelChanges.chooseModel")}</option>
          {visibleModels.map(model => <option key={model} value={model}>{model}{candidates.has(model) ? ` — ${t("modelChanges.added")}` : ""}</option>)}
        </select></label>
        {!inventory.isPending && !inventory.isError && !models.length ? <p className="muted">{t("modelChanges.noTargets")}</p> : null}
        <h3>{t("modelChanges.members")}</h3>
        <div className="model-replacement-members">{members.map(member => <label className="check" key={member.member_id}><input type="checkbox" checked={memberIds.includes(member.member_id)} onChange={e => { setMemberIds(ids => e.target.checked ? [...ids, member.member_id] : ids.filter(id => id !== member.member_id)); prepare.reset(); }} /><span><strong className="mono">{member.model_pattern}</strong><small>{member.route_name} · {t("modelChanges.member", { id: member.member_id, group: member.group_name || "default" })}</small></span></label>)}</div>
      </fieldset>
      {prepare.error ? <div className="inline-error" role="alert">{String(prepare.error)}</div> : null}
      <div className="dialog-actions"><Button variant="secondary" disabled={busy} onClick={onClose}>{t("common.cancel")}</Button><Button disabled={busy || !memberIds.length || !models.includes(targetModel) || inventory.isError} onClick={() => prepare.mutate({ change_ids: changes.map(change => change.id), member_ids: memberIds, target_channel_id: targetChannel, target_model: targetModel })}>{busy ? t("common.working") : t("modelChanges.preview")}</Button></div>
    </>}
  </Dialog>;
}
