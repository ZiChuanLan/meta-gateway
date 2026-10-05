import { useQuery } from "@tanstack/react-query";
import { Send } from "lucide-react";
import { useState } from "react";
import { Button, Field, Page, Panel } from "../components/ui";
import { useI18n } from "../i18n";
import { accountRequest } from "../team/transport";
import { teamError, teamText } from "../team/text";
import type { UserKey, UserModel } from "../team/types";
import { ImagePanel } from "./ImagePanel";

/** What one probe came back with: the upstream's text plus its own usage. */
interface ProbeResult {
  content: string;
  promptTokens?: number;
  completionTokens?: number;
  totalTokens?: number;
}

/** The slice of a chat completion this page reads. */
interface ChatPayload {
  choices?: Array<{ message?: { content?: string } }>;
  usage?: {
    prompt_tokens?: number;
    completion_tokens?: number;
    total_tokens?: number;
  };
  error?: { message?: string };
  message?: string;
}

/**
 * The member's workbench: a probe with the member's own token.
 *
 * This is deliberately NOT the console's /admin/try/* transplanted. That
 * endpoint authenticates with the admin bearer and bypasses the downstream
 * token entirely — which also bypasses every quota and the billing that goes
 * with them. What a member wants is the opposite: a call that is identical to
 * the one their own code will make, so that metering, quota, failover and the
 * request log all behave exactly as they will in production. The price of that
 * honesty is that a probe really does spend the member's credit, which is why
 * the page says so before the first request.
 *
 * It also means there is no new relay path to keep in sync: the browser posts
 * to /v1 like any other client, with a token the member can already reveal
 * (/me/keys/{id}/reveal).
 */
export function WorkbenchPage() {
  const { locale } = useI18n();
  const t = teamText(locale);
  // Which tool the page is showing. Both are probes with the member's own
  // token; the difference is the endpoint, not the principle.
  const [tool, setTool] = useState<"text" | "image">("text");
  const [chosenKey, setChosenKey] = useState(0);
  const [chosenModel, setChosenModel] = useState("");
  const [prompt, setPrompt] = useState("Say hello in one short sentence.");
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [result, setResult] = useState<ProbeResult | null>(null);

  const keys = useQuery({
    queryKey: ["me", "keys", "workbench"],
    queryFn: ({ signal }) => accountRequest<UserKey[]>("/me/keys", { signal }),
  });
  const models = useQuery({
    queryKey: ["me", "model-catalog", "workbench"],
    queryFn: ({ signal }) =>
      accountRequest<UserModel[]>("/me/model-catalog", { signal }),
  });

  const usable = (keys.data ?? []).filter((key) => key.enabled);
  const activeKey = chosenKey || usable[0]?.id || 0;
  const modelNames = (models.data ?? []).map((model) => model.name);
  const activeModel = chosenModel || modelNames[0] || "";

  async function send() {
    if (!activeKey || !activeModel) return;
    setPending(true);
    setError(null);
    setResult(null);
    try {
      // The plaintext is fetched once per probe and lives only in this request:
      // a member may reveal their own token, which is what lets the probe be an
      // ordinary /v1 call instead of a gateway-side impersonation.
      const { token } = await accountRequest<{ token: string }>(
        `/me/keys/${activeKey}/reveal`,
        { method: "POST" },
      );
      const response = await fetch("/v1/chat/completions", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          Authorization: `Bearer ${token}`,
        },
        body: JSON.stringify({
          model: activeModel,
          messages: [{ role: "user", content: prompt }],
        }),
      });
      const text = await response.text();
      let payload: ChatPayload | null = null;
      try {
        payload = JSON.parse(text) as ChatPayload;
      } catch {
        // Upstreams do return non-JSON bodies on some failures; the raw text is
        // still the most useful thing to show.
      }
      if (!response.ok) {
        throw new Error(
          payload?.error?.message ||
            payload?.message ||
            text.slice(0, 400) ||
            `HTTP ${response.status}`,
        );
      }
      const usage = payload?.usage ?? {};
      setResult({
        content: payload?.choices?.[0]?.message?.content ?? text,
        promptTokens: usage.prompt_tokens,
        completionTokens: usage.completion_tokens,
        totalTokens: usage.total_tokens,
      });
    } catch (failure) {
      setError(failure);
    } finally {
      setPending(false);
    }
  }

  return (
    <Page
      kicker={t("workbench")}
      title={t("workbench")}
      description={t("wbIntro")}
    >
      <div className="tabs wb-mode-tabs">
        <button
          type="button"
          aria-pressed={tool === "text"}
          onClick={() => setTool("text")}
        >
          {t("wbText")}
        </button>
        <button
          type="button"
          aria-pressed={tool === "image"}
          onClick={() => setTool("image")}
        >
          {t("wbImage")}
        </button>
      </div>
      {tool === "image" ? (
        <ImagePanel keys={usable} />
      ) : (
      <div className="wb-grid">
        <Panel className="wb-compose">
          <div className="panel-header">
            <strong>{t("wbPrompt")}</strong>
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
          {!keys.isPending && usable.length === 0 ? (
            <p className="team-muted">{t("wbNoToken")}</p>
          ) : null}
          <Field label={t("wbModel")}>
            <select
              value={activeModel}
              disabled={pending || modelNames.length === 0}
              onChange={(event) => setChosenModel(event.target.value)}
            >
              {modelNames.map((name) => (
                <option key={name} value={name}>
                  {name}
                </option>
              ))}
            </select>
          </Field>
          <Field label={t("wbPrompt")}>
            <textarea
              rows={6}
              value={prompt}
              disabled={pending}
              onChange={(event) => setPrompt(event.target.value)}
            />
          </Field>
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
            <strong>{t("wbResult")}</strong>
          </div>
          {result ? (
            <>
              <pre className="wb-answer">{result.content}</pre>
              <div className="wb-usage">
                <strong>{t("wbUsage")}</strong>
                <span>
                  prompt {result.promptTokens ?? "—"} · completion{" "}
                  {result.completionTokens ?? "—"} · total {result.totalTokens ?? "—"}
                </span>
                <small className="team-muted">{t("wbUsageNote")}</small>
              </div>
            </>
          ) : (
            <p className="team-muted">{t("wbEmpty")}</p>
          )}
        </Panel>
      </div>
      )}
    </Page>
  );
}
