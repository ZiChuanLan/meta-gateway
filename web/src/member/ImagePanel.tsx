import { useQuery } from "@tanstack/react-query";
import { ImageIcon, Send } from "lucide-react";
import { useState } from "react";
import { Button, Field, Panel } from "../components/ui";
import { useI18n } from "../i18n";
import { accountRequest } from "../team/transport";
import { teamError, teamText } from "../team/text";
import type { UserKey, UserModel } from "../team/types";

/**
 * The image half of the member's workbench.
 *
 * Same principle as the text probe next door: the browser posts to /v1 with the
 * member's own token, so metering, quota and the request log all behave exactly
 * as they will for the member's own code. Nothing here talks to /admin/try/* —
 * that path authenticates as the operator and bypasses every quota.
 *
 * Editing is multipart because that is what the protocol is: an image plus a
 * prompt. The gateway's own image path is a byte-for-byte pass-through with no
 * retries (it is a non-idempotent write), and this panel adds no interpretation
 * of its own — it sends what the operator typed and shows what came back.
 */
export function ImagePanel({ keys }: { keys: UserKey[] }) {
  const { locale } = useI18n();
  const t = teamText(locale);
  const [chosenKey, setChosenKey] = useState(0);
  const [chosenModel, setChosenModel] = useState("");
  const [mode, setMode] = useState<"generate" | "edit">("generate");
  const [prompt, setPrompt] = useState("A red panda reading a newspaper, watercolor");
  const [size, setSize] = useState("1024x1024");
  const [file, setFile] = useState<File | null>(null);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [images, setImages] = useState<string[]>([]);
  const [note, setNote] = useState("");

  // Only models that say they produce images: offering a text model here would
  // send every probe down a 400.
  const models = useQuery({
    queryKey: ["me", "model-catalog", "images"],
    queryFn: ({ signal }) =>
      accountRequest<UserModel[]>("/me/model-catalog", { signal }),
  });
  const imageModels = (models.data ?? []).filter((model) =>
    (model.output_modalities || "").toLowerCase().includes("image"),
  );
  const usable = keys.filter((key) => key.enabled);
  const activeKey = chosenKey || usable[0]?.id || 0;
  const activeModel = chosenModel || imageModels[0]?.name || "";

  async function revealToken(keyID: number) {
    const { token } = await accountRequest<{ token: string }>(
      `/me/keys/${keyID}/reveal`,
      { method: "POST" },
    );
    return token;
  }

  async function send() {
    if (!activeKey || !activeModel) return;
    setPending(true);
    setError(null);
    setImages([]);
    setNote("");
    try {
      const token = await revealToken(activeKey);
      const endpoint = mode === "edit" ? "/v1/images/edits" : "/v1/images/generations";
      let response: Response;
      if (mode === "edit") {
        if (!file) {
          setPending(false);
          setError(new Error(t("imgNoReference")));
          return;
        }
        const body = new FormData();
        body.append("model", activeModel);
        body.append("prompt", prompt);
        body.append("image", file);
        response = await fetch(endpoint, {
          method: "POST",
          headers: { Authorization: `Bearer ${token}` },
          body,
        });
      } else {
        response = await fetch(endpoint, {
          method: "POST",
          headers: {
            "Content-Type": "application/json",
            Authorization: `Bearer ${token}`,
          },
          body: JSON.stringify({ model: activeModel, prompt, size }),
        });
      }
      const text = await response.text();
      let payload: {
        data?: Array<{ b64_json?: string; url?: string }>;
        usage?: Record<string, unknown>;
        error?: { message?: string };
        message?: string;
      } | null = null;
      try {
        payload = JSON.parse(text);
      } catch {
        // Some upstreams answer with plain text on failure; showing it beats
        // swallowing it.
      }
      if (!response.ok) {
        throw new Error(
          payload?.error?.message ||
            payload?.message ||
            text.slice(0, 400) ||
            `HTTP ${response.status}`,
        );
      }
      const rendered = (payload?.data ?? [])
        .map((entry) =>
          entry.b64_json ? `data:image/png;base64,${entry.b64_json}` : (entry.url ?? ""),
        )
        .filter(Boolean);
      setImages(rendered);
      if (rendered.length === 0) setNote(text.slice(0, 800));
    } catch (failure) {
      setError(failure);
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="wb-grid">
      <Panel className="wb-compose">
        <div className="panel-header">
          <strong>{t("imgTitle")}</strong>
          <div className="tabs wb-tabs">
            <button
              type="button"
              aria-pressed={mode === "generate"}
              onClick={() => setMode("generate")}
            >
              {t("imgGenerate")}
            </button>
            <button
              type="button"
              aria-pressed={mode === "edit"}
              onClick={() => setMode("edit")}
            >
              {t("imgEdit")}
            </button>
          </div>
        </div>
        <Field label={t("wbToken")}>
          <select
            value={activeKey}
            disabled={pending || usable.length === 0}
            onChange={(event) => setChosenKey(Number(event.target.value))}
          >
            {usable.map((key) => (
              <option key={key.id} value={key.id}>
                {key.name}
              </option>
            ))}
          </select>
        </Field>
        {usable.length === 0 ? <p className="team-muted">{t("wbNoToken")}</p> : null}
        <Field label={t("wbModel")}>
          <select
            value={activeModel}
            disabled={pending || imageModels.length === 0}
            onChange={(event) => setChosenModel(event.target.value)}
          >
            {imageModels.map((model) => (
              <option key={model.name} value={model.name}>
                {model.name}
              </option>
            ))}
          </select>
        </Field>
        {models.isSuccess && imageModels.length === 0 ? (
          <p className="team-muted">{t("imgNoModels")}</p>
        ) : null}
        <Field label={t("wbPrompt")}>
          <textarea
            rows={4}
            value={prompt}
            disabled={pending}
            onChange={(event) => setPrompt(event.target.value)}
          />
        </Field>
        {mode === "edit" ? (
          <Field label={t("imgReference")} hint={t("imgReferenceHint")}>
            <input
              type="file"
              accept="image/png,image/jpeg,image/webp"
              disabled={pending}
              onChange={(event) => setFile(event.target.files?.[0] ?? null)}
            />
          </Field>
        ) : (
          <Field label={t("imgSize")}>
            <select
              value={size}
              disabled={pending}
              onChange={(event) => setSize(event.target.value)}
            >
              {["1024x1024", "1024x1536", "1536x1024", "512x512"].map((option) => (
                <option key={option} value={option}>
                  {option}
                </option>
              ))}
            </select>
          </Field>
        )}
        {error ? (
          <div role="alert" className="team-error">
            {teamError(error, locale)}
          </div>
        ) : null}
        <div className="team-actions">
          <Button
            icon={<Send size={15} />}
            disabled={pending || !activeKey || !activeModel}
            onClick={() => void send()}
          >
            {pending ? t("wbSending") : t("wbSend")}
          </Button>
        </div>
      </Panel>
      <Panel className="wb-result">
        <div className="panel-header">
          <strong>{t("imgResult")}</strong>
        </div>
        {images.length > 0 ? (
          <div className="wb-images">
            {images.map((source, index) => (
              <img key={index} src={source} alt={prompt} />
            ))}
          </div>
        ) : note ? (
          <pre className="wb-answer">{note}</pre>
        ) : (
          <p className="team-muted">
            <ImageIcon size={14} /> {t("imgEmpty")}
          </p>
        )}
      </Panel>
    </div>
  );
}
