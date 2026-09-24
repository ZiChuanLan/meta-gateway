import { useMemo, useState } from "react";
import { api } from "../api/client";
import type { Route, RoutingCandidate } from "../api/types";
import { Button, Field, ErrorState } from "../components/ui";
import { useI18n } from "../i18n";
import { upstreamChoices } from "./models/routingPolicy";
import { useSession } from "../session";

/**
 * Admin console probe: no downstream API key required.
 * Uses admin Bearer → POST /admin/try/chat.
 * Same model name on multiple channels: pick upstream, or leave "auto" for gateway routing.
 *
 * The picker lists MEMBERS, not channels: a unified alias can hold several
 * upstream 原模型 names on one channel, and only a member pin can test the row
 * the operator picked.
 */
export function TryPanel({
  defaultModel,
  members = [],
  route,
  onClose,
}: {
  defaultModel: string;
  /** Members serving this model (multi-channel / multi-原模型 same name). */
  members?: RoutingCandidate[];
  /** The route those members belong to; carries the legacy alias mapping. */
  route?: Route;
  onClose?: () => void;
}) {
  const { t } = useI18n();
  const { client } = useSession();
  const service = api(client!);
  const [model, setModel] = useState(defaultModel);
  const [prompt, setPrompt] = useState("Say hello in one short sentence.");
  const [memberId, setMemberId] = useState<number>(0);
  const [pending, setPending] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [result, setResult] = useState("");
  const [meta, setMeta] = useState("");

  const upstreams = useMemo(
    () => upstreamChoices(members, route, t),
    [members, route, t],
  );

  const multiUpstream = upstreams.length > 1;

  async function run() {
    if (!model.trim()) return;
    setPending(true);
    setError(null);
    setResult("");
    setMeta("");
    try {
      const response = await service.tryChat({
        model: model.trim(),
        prompt,
        member_id: memberId > 0 ? memberId : undefined,
      });
      // The channel name does not identify the upstream when an alias maps to
      // several 原模型 on one channel, so the result names the model that was
      // actually sent — the only way to see what a pinned row really tested.
      const via =
        response.channel_name != null
          ? t("try.viaChannel", {
              name: response.channel_name,
              id: response.channel_id ?? "—",
            })
          : t("try.viaAuto");
      const origin =
        response.upstream_model && response.upstream_model !== response.model
          ? t("routing.memberOrigin", { model: response.upstream_model })
          : "";
      setMeta(
        [
          t("try.meta", {
            status: response.status,
            latency: response.latency_ms,
            model: response.model,
            via,
          }),
          origin,
        ]
          .filter(Boolean)
          .join(" · "),
      );
      setResult(JSON.stringify(response.body, null, 2));
      if (response.status < 200 || response.status >= 300) {
        setError(
          new Error(t("try.upstreamStatus", { status: response.status })),
        );
      }
    } catch (err) {
      setError(err);
    } finally {
      setPending(false);
    }
  }

  return (
    <div className="try-box">
      <div className="ops-panel-context">
        <span>{multiUpstream ? t("try.hintMulti") : t("try.hint")}</span>
      </div>
      <Field label={t("common.model")}>
        <input
          value={model}
          onChange={(e) => setModel(e.target.value)}
          className="mono"
        />
      </Field>
      {upstreams.length > 0 ? (
        <Field
          label={t("try.upstream")}
          hint={
            multiUpstream
              ? t("try.upstreamHintMulti")
              : t("try.upstreamHintOne")
          }
        >
          <select
            aria-label={t("try.upstream")}
            value={memberId}
            onChange={(e) => setMemberId(Number(e.target.value) || 0)}
          >
            <option value={0}>{t("try.upstreamAuto")}</option>
            {upstreams.map((upstream) => (
              <option key={upstream.memberId} value={upstream.memberId}>
                {upstream.label}
              </option>
            ))}
          </select>
        </Field>
      ) : null}
      <Field label={t("try.prompt")}>
        <textarea value={prompt} onChange={(e) => setPrompt(e.target.value)} />
      </Field>
      {error ? <ErrorState error={error} /> : null}
      {!error && meta ? (
        <div className="result-strip result-strip-info">{meta}</div>
      ) : null}
      {result ? (
        error ? (
          <details className="try-result-details">
            <summary>{t("try.responseDetails")}</summary>
            {meta ? <p>{meta}</p> : null}
            <pre className="try-result">{result}</pre>
          </details>
        ) : (
          <pre className="try-result">{result}</pre>
        )
      ) : null}
      <div className="detail-actions" style={{ marginTop: 12 }}>
        {onClose ? (
          <Button variant="secondary" onClick={onClose}>
            {t("common.close")}
          </Button>
        ) : null}
        <Button disabled={pending || !model.trim()} onClick={() => void run()}>
          {pending ? t("common.working") : t("try.send")}
        </Button>
      </div>
    </div>
  );
}
