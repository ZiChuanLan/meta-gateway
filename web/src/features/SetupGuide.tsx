import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
import { Link } from "react-router-dom";
import { Check, Copy } from "lucide-react";
import { api } from "../api/client";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { Panel } from "../components/ui";

const DISMISS_KEY = "mg.setup-guide.dismissed";

/**
 * First-run setup guide, new-api style: a four-step checklist that shows up
 * on the dashboard while the gateway has no upstream connections yet and
 * walks the operator from "add a connection" to "first successful call".
 * Each step checks real state (channels, adopted models, downstream keys,
 * relay logs), so ticks reflect reality rather than click-through.
 *
 * Reopen any time with ?setup=1; dismissal persists in localStorage.
 */
export function SetupGuide() {
  const { client } = useSession();
  const s = api(client!);
  const { t } = useI18n();
  const [dismissed, setDismissed] = useState(
    () => localStorage.getItem(DISMISS_KEY) === "1",
  );
  const [forced] = useState(
    () => new URLSearchParams(window.location.search).get("setup") === "1",
  );
  const [copied, setCopied] = useState(false);

  const channels = useQuery({
    queryKey: ["channel-overviews"],
    queryFn: ({ signal }) => s.channelOverviews(signal),
  });
  const keys = useQuery({
    queryKey: ["keys"],
    queryFn: ({ signal }) => s.keys(signal),
  });
  const logs = useQuery({
    queryKey: ["proxy-logs", { limit: 10 }],
    queryFn: ({ signal }) => s.proxyLogs({ limit: 10 }, signal),
  });

  const channelList = channels.data ?? [];
  const fresh = channels.isSuccess && channelList.length === 0;
  if (!forced && (!fresh || dismissed)) return null;

  const hasChannel = channelList.length > 0;
  const hasModels = channelList.some((row) => row.model_count > 0);
  const hasKey = (keys.data ?? []).length > 0;
  const hasCall = (logs.data ?? []).some(
    (row) => row.status >= 200 && row.status < 300,
  );

  const steps = [
    {
      done: hasChannel,
      title: t("setup.step1Title"),
      desc: t("setup.step1Desc"),
      to: "/channels",
    },
    {
      done: hasModels,
      title: t("setup.step2Title"),
      desc: t("setup.step2Desc"),
      to: "/channels",
    },
    {
      done: hasKey,
      title: t("setup.step3Title"),
      desc: t("setup.step3Desc"),
      to: "/keys",
    },
    {
      done: hasCall,
      title: t("setup.step4Title"),
      desc: t("setup.step4Desc"),
      to: null,
    },
  ];
  const doneCount = steps.filter((step) => step.done).length;
  const allDone = doneCount === steps.length;
  const curl = `curl ${window.location.origin}/v1/chat/completions \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer sk-..." \\
  -d '{"model":"<model>","messages":[{"role":"user","content":"hi"}]}'`;

  const copyCurl = async () => {
    try {
      await navigator.clipboard.writeText(curl);
      setCopied(true);
      setTimeout(() => setCopied(false), 1500);
    } catch {
      // Clipboard unavailable (permissions); the text stays selectable.
    }
  };

  const dismiss = () => {
    localStorage.setItem(DISMISS_KEY, "1");
    setDismissed(true);
  };

  return (
    <Panel
      className="setup-guide"
      title={t("setup.title")}
      actions={
        <div className="setup-guide-actions">
          <span className="setup-guide-progress">
            {t("setup.progress", { done: doneCount, total: steps.length })}
          </span>
          <button
            type="button"
            className="setup-guide-dismiss"
            onClick={dismiss}
          >
            {t("setup.dismiss")}
          </button>
        </div>
      }
    >
      {allDone ? <p className="setup-guide-complete">{t("setup.allDone")}</p> : null}
      <ol className="setup-guide-steps">
        {steps.map((step, index) => (
          <li key={step.title} className={step.done ? "is-done" : ""}>
            <span className="setup-guide-check" aria-hidden>
              {step.done ? <Check size={13} /> : index + 1}
            </span>
            <div className="setup-guide-body">
              <strong>{step.title}</strong>
              <p>{step.desc}</p>
            </div>
            {step.to && !step.done ? (
              <Link className="setup-guide-go" to={step.to}>
                {t("setup.go")}
              </Link>
            ) : null}
          </li>
        ))}
      </ol>
      <div className="setup-guide-curl">
        <div className="setup-guide-curl-head">
          <span>curl</span>
          <button type="button" onClick={copyCurl}>
            <Copy size={12} />
            {copied ? t("setup.copied") : t("setup.copy")}
          </button>
        </div>
        <pre>{curl}</pre>
      </div>
    </Panel>
  );
}
