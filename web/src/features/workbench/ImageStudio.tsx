import { useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { RotateCcw, Sparkles, Trash2 } from "lucide-react";
import { api } from "../../api/client";
import { SearchableSelect } from "../../components/SearchableSelect";
import { Button, Empty, ErrorState, Field, IconButton, Panel, formatDate } from "../../components/ui";
import { useI18n } from "../../i18n";
import { upstreamMessage } from "../../lib/upstreamError";
import { primaryChannelName, upstreamChoices } from "../models/routingPolicy";
import { useSession } from "../../session";
import {
  MAX_RUNS,
  loadImageForm,
  loadRuns,
  saveImageForm,
  saveRuns,
  type WorkbenchImage,
  type WorkbenchRun,
} from "./workbenchState";

const IMAGE_KINDS = new Set(["image_gen", "image_edit"]);
const MAX_UPLOAD_BYTES = 20 * 1024 * 1024;

type ReferenceImage = { name: string; dataUrl: string; size: number };
type ImageResult = WorkbenchImage;
type RunResult = WorkbenchRun;
type ImageRequest = Parameters<ReturnType<typeof api>["tryImage"]>[0];

let runSeq = 0;
const runID = () => `run-${Date.now().toString(36)}-${++runSeq}`;

function imageSource(image?: ImageResult) {
  const source = (image?.data_url || image?.url || "").trim();
  return /^(https?:\/\/|data:image\/(?:png|jpe?g|webp|gif|avif);base64,)/i.test(source)
    ? source
    : "";
}

export default function ImageStudio({ active }: { active: boolean }) {
  const { t } = useI18n();
  const { client } = useSession();
  const service = api(client!);
  const fileInput = useRef<HTMLInputElement>(null);
  const readingFiles = useRef(false);
  const [reading, setReading] = useState(false);
  const [model, setModel] = useState("");
  const [memberId, setMemberId] = useState(0);
  const [mode, setMode] = useState<"auto" | "generate" | "edit">("auto");
  const [prompt, setPrompt] = useState("");
  const [size, setSize] = useState("");
  const [refs, setRefs] = useState<ReferenceImage[]>([]);
  const [error, setError] = useState<unknown>(null);
  const [feedback, setFeedback] = useState("");
  const [latest, setLatest] = useState<RunResult | null>(null);
  const [history, setHistory] = useState<RunResult[]>([]);
  const [historyLoaded, setHistoryLoaded] = useState(false);
  const [persistWarning, setPersistWarning] = useState("");
  // The save callback needs the newest list, not the one captured when the
  // mutation was created.
  const historyRef = useRef<RunResult[]>([]);

  // The workbench is a workspace, not a one-shot form: the last generation and
  // the request form come back on the next visit. The panel keeps working while
  // the store is still opening (it is asynchronous), and a stored value never
  // overwrites something the operator already typed.
  useEffect(() => {
    let cancelled = false;
    void (async () => {
      const [runs, form] = await Promise.all([loadRuns(), loadImageForm()]);
      if (cancelled) return;
      if (runs.length) {
        setHistory(runs);
        setLatest((current) => current ?? runs[0]!);
      }
      if (form) {
        setModel((current) => current || form.model);
        setMode((current) => (current === "auto" && form.mode ? (form.mode as typeof current) : current));
        setSize((current) => current || form.size);
        setPrompt((current) => current || form.prompt);
      }
      setHistoryLoaded(true);
    })();
    return () => { cancelled = true; };
  }, []);
  useEffect(() => {
    if (!historyLoaded) return;
    const timer = window.setTimeout(() => {
      void saveImageForm({ model, mode, size, prompt });
    }, 300);
    return () => window.clearTimeout(timer);
  }, [historyLoaded, model, mode, size, prompt]);
  useEffect(() => { historyRef.current = history; }, [history]);

  const routes = useQuery({
    queryKey: ["route-overviews"],
    queryFn: ({ signal }) => service.routeOverviews(signal),
    enabled: active,
  });
  const modelNames = useMemo(() => Array.from(new Set((routes.data ?? [])
    .filter(({ route }) => route.enabled && !/[*?]/.test(route.model_pattern))
    .map(({ route }) => route.model_pattern))).sort(), [routes.data]);
  const capabilities = useQuery({
    queryKey: ["capabilities", modelNames],
    queryFn: () => service.resolveModelCapabilities(modelNames),
    enabled: active && modelNames.length > 0,
  });
  const imageModels = useMemo(() => modelNames.filter((name) => {
    const capability = capabilities.data?.items[name];
    return capability && IMAGE_KINDS.has(capability.kind);
  }), [modelNames, capabilities.data]);
  const activeModel = imageModels.includes(model) ? model : imageModels[0] ?? "";
  const modelSites = useMemo(() => {
    const map = new Map<string, string>();
    for (const overview of routes.data ?? []) {
      const site = primaryChannelName(overview);
      if (site) map.set(overview.route.model_pattern, site);
    }
    return map;
  }, [routes.data]);
  // The option label carries the serving connection, and the picker searches
  // labels — so "which site serves this model?" is answerable here, and typing
  // a site name filters down to the models it serves.
  const modelOptions = useMemo(
    () => imageModels.map((name) => {
      const site = modelSites.get(name);
      return { value: name, label: site ? `${name} · ${site}` : name };
    }),
    [imageModels, modelSites],
  );
  // Image upstreams charge per plane, so pinning one is the way to test that
  // specific path; mirror the Playground's connection picker — including the
  // 原模型 in the label, since one channel can serve the alias under several
  // upstream names.
  const upstreams = useMemo(() => {
    const overview = (routes.data ?? []).find(
      ({ route }) => route.model_pattern === activeModel,
    );
    return overview
      ? upstreamChoices(overview.members ?? [], overview.route, t)
      : [];
  }, [routes.data, activeModel, t]);
  const capability = capabilities.data?.items[activeModel];
  const endpoints = capability?.endpoints ?? [];
  const chatImages = endpoints.includes("/v1/chat/completions");
  const canGenerate = endpoints.includes("/v1/images/generations") || chatImages;
  const canEdit = endpoints.includes("/v1/images/edits") ||
    (chatImages && !!capability?.input_modalities.includes("image"));
  const maxImages = capability?.max_input_images ?? 0;
  const editing = mode === "edit" || (mode === "auto" && (refs.length > 0 || !canGenerate));
  const sizeOptions = (capability?.size_options ?? "").split(",").map((value) => value.trim()).filter(Boolean);

  let inputIssue = "";
  if (maxImages > 0 && refs.length > maxImages) {
    inputIssue = t("workbench.image.maxImagesExceeded", { n: maxImages });
  } else if (mode === "generate" && refs.length > 0) {
    inputIssue = t("workbench.image.chooseEdit");
  } else if ((mode === "edit" || (!canGenerate && mode === "auto")) && refs.length === 0) {
    inputIssue = t("workbench.image.referencesRequired");
  } else if (editing && !canEdit) {
    inputIssue = t("workbench.image.editUnsupported");
  } else if (!editing && !canGenerate) {
    inputIssue = t("workbench.image.generateUnsupported");
  }

  const run = useMutation({
    gcTime: 0,
    mutationFn: (request: ImageRequest) => service.tryImage(request),
    onMutate: () => { setError(null); setFeedback(""); },
    onSuccess: (data, request) => {
      // The success path drops `body` (it duplicates a large base64 image), so
      // read the upstream's own words only where the call actually failed.
      const refused = data.status < 200 || data.status >= 300;
      const detail = refused || (data.images ?? []).length === 0
        ? upstreamMessage(data.body)
        : "";
      const result: RunResult = {
        id: runID(),
        at: new Date().toISOString(), model: data.model || request.model,
        status: data.status, latencyMs: data.latency_ms, channelName: data.channel_name,
        endpoint: data.plan?.endpoint ?? "", format: data.plan?.format ?? "",
        prompt: request.prompt ?? "", mode: request.mode ?? "auto", size: request.size,
        images: (data.images ?? []).filter((image) => imageSource(image)),
        upstreamError: detail || undefined,
      };
      setLatest(result);
      if (refused) {
        setFeedback(detail || t("workbench.image.upstreamError", { status: data.status }));
        return;
      }
      if (result.images.length === 0) {
        setFeedback(detail || t("workbench.image.upstreamStatus", { status: data.status }));
        return;
      }
      // Only a run that produced an image belongs in the history strip; that
      // is what the thumbnails show. The store trims by its own budget and
      // reports back, so an oversized image is never silently "saved".
      const next = [result, ...historyRef.current];
      setHistory(next.slice(0, MAX_RUNS));
      void saveRuns(next).then((kept) => {
        setPersistWarning(kept[0]?.id === result.id ? "" : t("workbench.image.historyTruncated"));
      });
    },
    onError: setError,
  });
  const busy = run.isPending || reading;
  // The history is the workbench's memory: every generation that produced an
  // image stays listed (and survives a reload), with the one on screen marked.
  // It used to hide itself with a single entry, which made the feature look
  // like it did not exist at all.
  const showHistory = history.length > 0;

  /** Puts a past run back into the panel and its request back into the form. */
  function reuse(run: RunResult, restore: boolean) {
    setLatest(run);
    setError(null);
    setFeedback("");
    if (!restore) return;
    setModel(run.model);
    setMode((run.mode as typeof mode) || "auto");
    setSize(run.size ?? "");
    setPrompt(run.prompt ?? "");
  }

  function forget(id: string) {
    const next = historyRef.current.filter((item) => item.id !== id);
    historyRef.current = next;
    setHistory(next);
    setLatest((current) => (current && current.id === id ? next[0] ?? null : current));
    void saveRuns(next).then((kept) => { if (!kept.length) setPersistWarning(""); });
  }

  function forgetAll() {
    historyRef.current = [];
    setHistory([]);
    setLatest(null);
    setPersistWarning("");
    void saveRuns([]);
  }

  async function addFiles(files: File[]) {
    if (!files.length || readingFiles.current || run.isPending) return;
    setError(null);
    setFeedback("");
    if (maxImages > 0 && refs.length + files.length > maxImages) {
      setFeedback(t("workbench.image.maxImagesExceeded", { n: maxImages }));
      return;
    }
    const invalid = files.find((file) => !file.type.startsWith("image/"));
    if (invalid) {
      setFeedback(t("workbench.image.invalidFile", { name: invalid.name }));
      return;
    }
    const bytes = refs.reduce((total, image) => total + image.size, 0) + files.reduce((total, file) => total + file.size, 0);
    if (bytes > MAX_UPLOAD_BYTES) {
      setFeedback(t("workbench.image.uploadTooLarge", { mb: 20 }));
      return;
    }
    readingFiles.current = true;
    setReading(true);
    try {
      const added = await Promise.all(files.map((file) => new Promise<ReferenceImage>((resolve, reject) => {
        const reader = new FileReader();
        reader.onload = () => resolve({ name: file.name, dataUrl: String(reader.result), size: file.size });
        reader.onerror = () => reject(new Error(t("workbench.image.fileReadFailed", { name: file.name })));
        reader.onabort = () => reject(new Error(t("workbench.image.fileReadFailed", { name: file.name })));
        reader.readAsDataURL(file);
      })));
      setRefs((current) => [...current, ...added]);
    } catch (failure) {
      setFeedback(failure instanceof Error ? failure.message : t("workbench.image.fileReadFailed", { name: files[0]?.name ?? "" }));
    } finally {
      readingFiles.current = false;
      setReading(false);
    }
  }

  if (routes.isPending || (modelNames.length > 0 && capabilities.isPending)) {
    return <p className="muted" role="status">{t("common.working")}</p>;
  }
  if (routes.isError) return <ErrorState error={routes.error} />;
  if (modelNames.length > 0 && capabilities.isError) return <ErrorState error={capabilities.error} />;
  if (imageModels.length === 0) return <Panel><Empty>{t("workbench.image.noModels")}</Empty></Panel>;

  return (
    <div className="workbench-grid">
      <Panel title={t("workbench.image.requestTitle")}>
        <fieldset className="workbench-form" disabled={busy}>
          <div className="meta-form">
            <Field label={t("workbench.image.model")} hint={t("workbench.image.modelHint")}>
              <SearchableSelect
                ariaLabel={t("workbench.image.model")}
                options={modelOptions}
                value={activeModel}
                placeholder={t("workbench.image.model")}
                onChange={(next) => {
                  setModel(next); setSize(""); setMode("auto"); setError(null); setFeedback("");
                  // Pinned connections belong to the previous model's members.
                  setMemberId(0);
                }}
              />
            </Field>
            <Field
              label={t("workbench.image.upstream")}
              hint={upstreams.length > 1
                ? t("workbench.image.upstreamHint")
                : t("workbench.image.upstreamHintOne")}
            >
              <select
                aria-label={t("workbench.image.upstream")}
                value={memberId}
                onChange={(event) => { setMemberId(Number(event.target.value) || 0); setError(null); setFeedback(""); }}
              >
                <option value={0}>{t("workbench.image.upstreamAuto")}</option>
                {upstreams.map((upstream) => (
                  <option key={upstream.memberId} value={upstream.memberId}>
                    {upstream.label}
                  </option>
                ))}
              </select>
            </Field>
            <Field label={t("workbench.image.mode")}>
              <select aria-label={t("workbench.image.mode")} value={mode} onChange={(event) => { setMode(event.target.value as typeof mode); setError(null); setFeedback(""); }}>
                <option value="auto">{t("workbench.image.modeAuto")}</option>
                <option value="generate" disabled={!canGenerate}>{t("workbench.image.modeGenerate")}</option>
                <option value="edit" disabled={!canEdit}>{t("workbench.image.modeEdit")}</option>
              </select>
            </Field>
            <Field label={t("workbench.image.size")}>
              {sizeOptions.length > 0 ? (
                <select aria-label={t("workbench.image.size")} value={size} onChange={(event) => setSize(event.target.value)}>
                  <option value="">{t("workbench.image.sizeDefault")}</option>
                  {sizeOptions.map((option) => <option key={option} value={option}>{option}</option>)}
                </select>
              ) : <input aria-label={t("workbench.image.size")} value={size} placeholder="1024x1024" onChange={(event) => setSize(event.target.value)} />}
            </Field>
          </div>
          <Field label={t("workbench.image.prompt")}>
            <textarea aria-label={t("workbench.image.prompt")} rows={4} value={prompt} placeholder={t("workbench.image.promptPlaceholder")} onChange={(event) => setPrompt(event.target.value)} />
          </Field>
          <Field label={t("workbench.image.refs")} hint={maxImages > 0 ? t("workbench.image.refsHint", { n: maxImages }) : t("workbench.image.refsHintUnbounded")}>
            <div className="workbench-refs">
              {refs.map((image, index) => (
                <div className="workbench-ref" key={`${image.name}-${index}`}>
                  <img src={image.dataUrl} alt={image.name} />
                  <button type="button" className="workbench-ref-remove" aria-label={t("workbench.image.removeRef", { name: image.name })} onClick={() => {
                    setRefs((current) => current.filter((_, position) => position !== index)); setError(null); setFeedback("");
                  }}><Trash2 size={12} /></button>
                </div>
              ))}
              <button type="button" className="workbench-ref-add" disabled={!canEdit || (maxImages > 0 && refs.length >= maxImages)} onClick={() => fileInput.current?.click()}>
                + {t("workbench.image.addRef")}
              </button>
              <input ref={fileInput} type="file" aria-label={t("workbench.image.addRef")} accept="image/*" multiple hidden onChange={(event) => {
                const files = Array.from(event.target.files ?? []);
                event.target.value = "";
                void addFiles(files);
              }} />
            </div>
          </Field>
          {inputIssue ? <p className="panel-hint" role="status">{inputIssue}</p> : null}
          <details className="workbench-protocol-details">
            <summary>{t("workbench.image.protocol")}</summary>
            <p className="mono">{endpoints.join(" · ")}</p>
            <p className="muted">{capability?.input_formats.join(" / ")}</p>
            {capability?.notes ? <p className="workbench-notes">{capability.notes}</p> : null}
          </details>
          {error ? <ErrorState error={error} /> : null}
          {feedback ? <p className="inline-error" role="alert">{feedback}</p> : null}
          <div className="dialog-actions">
            <span className="flex-spacer" />
            <Button disabled={busy || !activeModel || !prompt.trim() || !!inputIssue} onClick={() => run.mutate({
              model: activeModel, prompt, mode, size: size || undefined,
              member_id: memberId > 0 ? memberId : undefined,
              images: refs.map((image) => ({ data_url: image.dataUrl, name: image.name })),
            })}>
              <Sparkles size={13} />
              {busy ? t("common.working") : t(editing ? "workbench.image.editRun" : "workbench.image.generateRun")}
            </Button>
          </div>
        </fieldset>
      </Panel>
      <Panel title={t("workbench.image.resultTitle")}>
        {run.isPending ? <p role="status" className="panel-hint">{t("workbench.image.waitHint")}</p> : null}
        {!latest ? <Empty>{t("workbench.image.empty")}</Empty> : null}
        {latest ? <>
          <div className="workbench-meta">
            <strong className="mono">{latest.model}</strong>
            <span>{t("workbench.image.latency", { ms: latest.latencyMs })}</span>
            {latest.channelName ? <span>{t("workbench.image.channel", { name: latest.channelName })}</span> : null}
            <span className="mono">{latest.endpoint} · {latest.format}</span>
            <span className="muted">{formatDate(latest.at)}</span>
          </div>
          <div className="workbench-gallery">
            {latest.images.length === 0 ? <p className="muted">{latest.upstreamError || t("workbench.image.upstreamStatus", { status: latest.status })}</p> : null}
            {latest.images.map((image, index) => {
              const source = imageSource(image);
              return <figure className="workbench-shot" key={index}>
                <a href={source} target="_blank" rel="noopener noreferrer" aria-label={t("workbench.image.preview")}>
                  <img src={source} alt={image.revised_prompt || t("workbench.image.resultAlt", { n: index + 1 })} loading="lazy" referrerPolicy="no-referrer" />
                </a>
                <figcaption>
                  <a href={source} download={`${latest.model}-${index + 1}`} target="_blank" rel="noopener noreferrer">{t("workbench.image.download")}</a>
                  {image.revised_prompt ? <span>{image.revised_prompt}</span> : null}
                </figcaption>
              </figure>;
            })}
          </div>
        </> : null}
        {showHistory ? <div className="workbench-history">
          <div className="workbench-history-head">
            <strong>{t("workbench.image.history")}</strong>
            <span className="muted">{t("workbench.image.historyCount", { count: history.length })}</span>
            <span className="flex-spacer" />
            <Button variant="quiet" icon={<Trash2 size={14} />} onClick={forgetAll}>{t("workbench.image.historyClear")}</Button>
          </div>
          {persistWarning ? <p className="panel-hint" role="status">{persistWarning}</p> : null}
          <ul>{history.map((item) => <li key={item.id} className={item.id === latest?.id ? "is-current" : undefined}>
            <button
              type="button"
              className="workbench-history-item"
              aria-pressed={item.id === latest?.id}
              aria-label={t("workbench.image.historyOpen", { model: item.model, time: formatDate(item.at) })}
              onClick={() => reuse(item, false)}
            >
              <img src={imageSource(item.images[0])} alt="" loading="lazy" referrerPolicy="no-referrer" />
              <span>
                <span className="workbench-history-prompt">{item.prompt || t("workbench.image.historyNoPrompt")}</span>
                <span className="mono">{item.model}</span>
                <span className="muted">{t("workbench.image.historyMeta", { n: item.images.length, ms: item.latencyMs, time: formatDate(item.at) })}</span>
              </span>
            </button>
            <span className="workbench-history-actions">
              <IconButton label={t("workbench.image.historyReuse")} onClick={() => reuse(item, true)}><RotateCcw size={13} /></IconButton>
              <IconButton label={t("workbench.image.historyDelete")} onClick={() => forget(item.id)}><Trash2 size={13} /></IconButton>
            </span>
          </li>)}</ul>
        </div> : null}
      </Panel>
    </div>
  );
}
