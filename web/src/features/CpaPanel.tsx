import { CheckCircle2, ExternalLink, Plus, RefreshCw, XCircle } from "lucide-react";
import { useState } from "react";
import { Link } from "react-router-dom";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api } from "../api/client";
import { Button, Page, Panel } from "../components/ui";
import { useAdminMutation } from "../hooks/useAdminMutation";
import { useI18n } from "../i18n";
import { useSession } from "../session";

const CPA_URL_KEY = "meta-gateway.cpa-url";
const DEFAULT_CPA_URL = "http://127.0.0.1:9090";

/**
 * CLIProxyAPI (OAuth subscription pool) integration page.
 * meta-gateway never manages the CPA process — this page surfaces its health
 * and wires it up as an upstream channel.
 */
export function CpaPanel() {
  const { t } = useI18n();
  const { client } = useSession();
  const qc = useQueryClient();
  const service = api(client!);
  const [baseUrl, setBaseUrl] = useState(
    () =>
      window.localStorage.getItem(CPA_URL_KEY) || DEFAULT_CPA_URL,
  );
  const [savedUrl, setSavedUrl] = useState(
    () =>
      window.localStorage.getItem(CPA_URL_KEY) || DEFAULT_CPA_URL,
  );

  const status = useQuery({
    queryKey: ["cpa-status", savedUrl],
    queryFn: ({ signal }) => service.cpaStatus(savedUrl, signal),
    refetchInterval: 10_000,
    retry: false,
  });

  const saveUrl = () => {
    const trimmed = baseUrl.trim() || DEFAULT_CPA_URL;
    window.localStorage.setItem(CPA_URL_KEY, trimmed);
    setBaseUrl(trimmed);
    setSavedUrl(trimmed);
  };

  const createChannel = useAdminMutation({
    mutationFn: () =>
      service.createChannel({
        name: "CLIProxyAPI (OAuth 池)",
        base_url: savedUrl,
        type_hint: "openai-compatible",
      }),
    invalidateKeys: [["channels"], ["channel-overviews"]],
  });

  const running = status.data?.running === true;

  return (
    <Page
      kicker={t("cpa.kicker")}
      title={t("cpa.title")}
      description={t("cpa.description")}
      actions={
        <Button
          variant="secondary"
          icon={<RefreshCw size={16} />}
          onClick={() => void status.refetch()}
        >
          {t("common.refresh")}
        </Button>
      }
    >
      <div className="stack">
        <Panel
          title={t("cpa.statusTitle")}
          titleHelp={t("cpa.statusHint")}
          actions={
            <span
              className={`cpa-status-chip ${running ? "is-running" : "is-stopped"}`}
            >
              {running ? (
                <>
                  <CheckCircle2 size={13} /> {t("cpa.running")}
                  {status.data?.latency_ms != null
                    ? ` · ${status.data.latency_ms}ms`
                    : ""}
                </>
              ) : (
                <>
                  <XCircle size={13} /> {t("cpa.notRunning")}
                </>
              )}
            </span>
          }
        >
          <div className="cpa-url-row">
            <input
              className="mono"
              value={baseUrl}
              onChange={(e) => setBaseUrl(e.target.value)}
              placeholder={DEFAULT_CPA_URL}
              spellCheck={false}
            />
            <Button variant="secondary" onClick={saveUrl}>
              {t("cpa.saveUrl")}
            </Button>
          </div>
          {!running && status.data?.error ? (
            <p className="muted" style={{ fontSize: 12, marginTop: 8 }}>
              {status.data.error}
            </p>
          ) : null}
          {!running ? (
            <div className="cpa-guide">
              <strong>{t("cpa.guideTitle")}</strong>
              <ol>
                <li>
                  {t("cpa.guideDownload")}{" "}
                  <a
                    href="https://github.com/router-for-me/CLIProxyAPI/releases"
                    target="_blank"
                    rel="noopener noreferrer"
                  >
                    GitHub Releases <ExternalLink size={12} />
                  </a>
                </li>
                <li>{t("cpa.guideRun")}</li>
                <li>
                  {t("cpa.guideConfigure")}{" "}
                  <code className="mono">127.0.0.1:9090</code>
                </li>
              </ol>
            </div>
          ) : null}
        </Panel>

        <Panel
          title={t("cpa.channelTitle")}
          titleHelp={t("cpa.channelHint")}
        >
          <p className="muted" style={{ fontSize: 13, lineHeight: 1.6 }}>
            {t("cpa.channelBody")}
          </p>
          <div className="cpa-actions">
            <Button
              icon={<Plus size={16} />}
              disabled={!running || createChannel.isPending}
              onClick={() => createChannel.mutate(undefined)}
            >
              {createChannel.isPending
                ? t("common.working")
                : t("cpa.createChannel")}
            </Button>
            <Link className="button button-secondary" to="/channels">
              {t("cpa.openChannels")}
            </Link>
          </div>
          {createChannel.isSuccess ? (
            <p className="result-strip" style={{ marginTop: 10 }}>
              <CheckCircle2 size={14} /> {t("cpa.channelCreated")}
            </p>
          ) : null}
        </Panel>
      </div>
    </Page>
  );
}

/** Admin route gate: visible only when the add-on is enabled (App handles nav). */
export default CpaPanel;
