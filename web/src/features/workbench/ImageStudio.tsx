import { useMemo, useRef, useState } from "react";
import { useMutation, useQuery } from "@tanstack/react-query";
import { Sparkles, Trash2 } from "lucide-react";
import { api } from "../../api/client";
import { Button, Empty, ErrorState, Field, Panel, formatDate } from "../../components/ui";
import { useI18n } from "../../i18n";
import { upstreamMessage } from "../../lib/upstreamError";
import { useSession } from "../../session";

const IMAGE_KINDS = new Set(["image_gen", "image_edit"]);
const MAX_UPLOAD_BYTES = 20 * 1024 * 1024;
const MAX_HISTORY_BYTES = 48 * 1024 * 1024;

type ReferenceImage = { name: string; dataUrl: string; size: number };
type ImageResult = { data_url?: string; url?: string; revised_prompt?: string };
type RunResult = {
  at: string;
  model: string;
  status: number;
  latencyMs: number;
  channelName?: string;
  endpoint: string;
  format: string;
  images: ImageResult[];
  /**
   * What the upstream actually said when it refused. Image upstreams spend
   * real quota and rate limits per plane, so "429" alone is not actionable —
   * the provider's own message is what tells an operator whether to wait.
   */
  upstreamError?: string;
};
type ImageRequest = Parameters<ReturnType<typeof api>["tryImage"]>[0];

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
  const [mode, setMode] = useState<"auto" | "generate" | "edit">("auto");
  const [prompt, setPrompt] = useState("");
  const [size, setSize] = useState("");
  const [refs, setRefs] = useState<ReferenceImage[]>([]);
  const [error, setError] = useState<unknown>(null);
  const [feedback, setFeedback] = useState("");
  const [latest, setLatest] = useState<RunResult | null>(null);
  const [history, setHistory] = useState<RunResult[]>([]);

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
        at: new Date().toISOString(), model: data.model || request.model,
        status: data.status, latencyMs: data.latency_ms, channelName: data.channel_name,
        endpoint: data.plan?.endpoint ?? "", format: data.plan?.format ?? "",
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
      setHistory((current) => {
        const retained: RunResult[] = [];
        let bytes = 0;
        for (const item of [result, ...current]) {
          const itemBytes = item.images.reduce((total, image) => total + imageSource(image).length, 0);
          if (retained.length >= 6 || (retained.length > 0 && bytes + itemBytes > MAX_HISTORY_BYTES)) break;
          retained.push(item);
          bytes += itemBytes;
        }
        return retained;
      });
    },
    onError: setError,
  });
  const busy = run.isPending || reading;

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
              <select aria-label={t("workbench.image.model")} value={activeModel} onChange={(event) => {
                setModel(event.target.value); setSize(""); setMode("auto"); setError(null); setFeedback("");
              }}>
                {imageModels.map((name) => <option key={name} value={name}>{name}</option>)}
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
        {history.length > 1 ? <div className="workbench-history">
          <strong>{t("workbench.image.history")}</strong>
          <ul>{history.map((item, index) => <li key={`${item.at}-${index}`}>
            <button type="button" className="workbench-history-item" aria-pressed={latest === item} onClick={() => setLatest(item)}>
              <img src={imageSource(item.images[0])} alt="" loading="lazy" referrerPolicy="no-referrer" />
              <span><span className="mono">{item.model}</span><span className="muted">{item.images.length} · {item.latencyMs}ms · {formatDate(item.at)}</span></span>
            </button>
          </li>)}</ul>
        </div> : null}
      </Panel>
    </div>
  );
}
