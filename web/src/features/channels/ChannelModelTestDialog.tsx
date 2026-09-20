import { CheckCircle2, Loader2, Play, Search, Square, XCircle } from "lucide-react";
import { useMemo, useRef, useState } from "react";
import { api } from "../../api/client";
import { Button, Dialog } from "../../components/ui";
import { useI18n } from "../../i18n";
import { useSession } from "../../session";

/**
 * How many upstream calls run at once. A smoke test must not become a load
 * test against the channel just because the operator selected every model, so
 * the ceiling lives on the client where the orchestration happens.
 */
const TEST_CONCURRENCY = 4;
const MAX_TOKENS_CEILING = 256;

type Verdict =
  | { status: "testing" }
  | { status: "ok"; statusCode: number; latencyMs: number }
  | { status: "failed"; statusCode: number; error: string };

/**
 * The connection drawer's 试调 action: one channel, its whole model inventory,
 * and one real upstream call per model.
 *
 * This is the answer to a workflow the Model Probe dialog cannot serve. That
 * dialog walks route members, so a model has to be adopted before it can be
 * probed — but the decision being made here is precisely *whether to adopt*,
 * and asking an operator to adopt forty candidates to find out which three
 * work is backwards. Every check therefore bypasses routing entirely and
 * writes nothing: no health row, no cooldown, no blacklist entry, no billing.
 * Closing the dialog throws the verdicts away, by design.
 */
export function ChannelModelTestDialog({
  channelId,
  channelName,
  models,
  onClose,
}: {
  channelId: number;
  channelName: string;
  /** The channel's own inventory; `adopted` only drives the badge. */
  models: Array<{ name: string; adopted: boolean }>;
  onClose: () => void;
}) {
  const { t } = useI18n();
  const { client } = useSession();
  const service = api(client!);

  const [query, setQuery] = useState("");
  const [maxTokens, setMaxTokens] = useState(1);
  const [onlyFailures, setOnlyFailures] = useState(false);
  const [verdicts, setVerdicts] = useState<Record<string, Verdict>>({});
  const [running, setRunning] = useState(false);
  const abortRef = useRef<AbortController | null>(null);
  const stoppedRef = useRef(false);

  const testOne = async (name: string, signal: AbortSignal) => {
    setVerdicts((current) => ({ ...current, [name]: { status: "testing" } }));
    try {
      const result = await service.tryChannelModel(
        { channel_id: channelId, model: name, max_tokens: maxTokens },
        signal,
      );
      setVerdicts((current) => ({
        ...current,
        [name]: result.ok
          ? {
              status: "ok",
              statusCode: result.status_code,
              latencyMs: result.latency_ms,
            }
          : {
              status: "failed",
              statusCode: result.status_code,
              error: result.error ?? "",
            },
      }));
    } catch (error) {
      // The request itself failed (offline, rejected, or abandoned). An abort
      // is the operator pressing stop, so it is not a verdict about a model.
      if (signal.aborted) return;
      setVerdicts((current) => ({
        ...current,
        [name]: {
          status: "failed",
          statusCode: 0,
          error: error instanceof Error ? error.message : String(error),
        },
      }));
    }
  };

  const run = async (names: string[]) => {
    if (names.length === 0 || running) return;
    const controller = new AbortController();
    abortRef.current = controller;
    stoppedRef.current = false;
    setRunning(true);

    let cursor = 0;
    const workers = Array.from(
      { length: Math.min(TEST_CONCURRENCY, names.length) },
      async () => {
        for (;;) {
          if (stoppedRef.current || controller.signal.aborted) return;
          const name = names[cursor];
          cursor += 1;
          if (name === undefined) return;
          await testOne(name, controller.signal);
        }
      },
    );
    await Promise.all(workers);

    setRunning(false);
    abortRef.current = null;
  };

  const stop = () => {
    stoppedRef.current = true;
    abortRef.current?.abort();
  };

  const close = () => {
    // Leaving mid-run would otherwise keep hammering the upstream for nothing.
    stop();
    onClose();
  };

  const needle = query.trim().toLowerCase();
  const visible = useMemo(() => {
    let list = models;
    if (needle) {
      list = list.filter((model) => model.name.toLowerCase().includes(needle));
    }
    if (onlyFailures) {
      list = list.filter(
        (model) => verdicts[model.name]?.status === "failed",
      );
    }
    return list;
  }, [models, needle, onlyFailures, verdicts]);

  const tally = useMemo(() => {
    let ok = 0;
    let failed = 0;
    for (const verdict of Object.values(verdicts)) {
      if (verdict.status === "ok") ok += 1;
      else if (verdict.status === "failed") failed += 1;
    }
    return { ok, failed, done: ok + failed };
  }, [verdicts]);

  const renderVerdict = (name: string) => {
    const verdict = verdicts[name];
    if (!verdict) {
      return <span className="channel-model-test-idle">{t("channels.testDialog.idle")}</span>;
    }
    if (verdict.status === "testing") {
      return (
        <span className="channel-model-test-idle">
          <Loader2 size={12} className="spin" />
          {t("channels.testDialog.testing")}
        </span>
      );
    }
    if (verdict.status === "ok") {
      return (
        <>
          <span className="channel-model-test-badge is-pass">
            <CheckCircle2 size={11} />
            {t("channels.testDialog.ok")}
          </span>
          <span className="channel-model-test-latency">{verdict.latencyMs} ms</span>
        </>
      );
    }
    return (
      <>
        <span className="channel-model-test-badge is-fail">
          <XCircle size={11} />
          {t("channels.testDialog.failed")}
        </span>
        {verdict.statusCode > 0 ? (
          <span className="channel-model-test-latency">{verdict.statusCode}</span>
        ) : null}
      </>
    );
  };

  return (
    <Dialog
      title={t("channels.testDialog.title", { name: channelName })}
      onClose={close}
      actions={
        <>
          <Button variant="secondary" disabled={running} onClick={close}>
            {t("common.close")}
          </Button>
          {running ? (
            <Button icon={<Square size={15} />} onClick={stop}>
              {t("channels.testDialog.stop")}
            </Button>
          ) : (
            <Button
              icon={<Play size={15} />}
              disabled={visible.length === 0}
              onClick={() => void run(visible.map((model) => model.name))}
            >
              {t("channels.testDialog.start")}
            </Button>
          )}
        </>
      }
    >
      <p className="unify-intro">{t("channels.testDialog.description")}</p>

      <div className="channel-model-test-bar">
        <div className="probe-search">
          <Search size={13} />
          <input
            type="search"
            value={query}
            placeholder={t("channels.testDialog.search")}
            onChange={(event) => setQuery(event.target.value)}
          />
        </div>
        <label
          className="channel-model-test-knob"
          title={t("channels.testDialog.maxTokensHint")}
        >
          <span>{t("channels.testDialog.maxTokens")}</span>
          <input
            type="number"
            min={1}
            max={MAX_TOKENS_CEILING}
            disabled={running}
            value={maxTokens}
            onChange={(event) => setMaxTokens(Number(event.target.value))}
          />
        </label>
        <button
          type="button"
          className="unify-covered-toggle"
          aria-pressed={onlyFailures}
          disabled={tally.failed === 0}
          onClick={() => setOnlyFailures((current) => !current)}
        >
          {onlyFailures
            ? t("channels.testDialog.showAll")
            : t("channels.testDialog.onlyFailures")}
        </button>
      </div>

      {/* The running total sits under the controls that produce it, so raising
          max tokens and retesting reads as a new measurement. */}
      <div className="probe-scope" role="status">
        {t("channels.testDialog.progress", {
          done: tally.done,
          total: models.length,
          ok: tally.ok,
          fail: tally.failed,
        })}
      </div>

      {visible.length === 0 ? (
        <p className="detail-section-empty is-quiet">
          {onlyFailures
            ? t("channels.testDialog.noFailures")
            : t("channels.testDialog.empty")}
        </p>
      ) : (
        <ul className="channel-model-list channel-model-test-list">
          {visible.map((model) => {
            const verdict = verdicts[model.name];
            return (
              <li key={model.name} className="channel-model-row">
                <span
                  className="mono truncate channel-model-test-name"
                  title={model.name}
                >
                  {model.name}
                </span>
                {model.adopted ? (
                  <span className="capability-chip is-key">
                    {t("channels.testDialog.adopted")}
                  </span>
                ) : null}
                <span className="channel-model-test-verdict">
                  {renderVerdict(model.name)}
                </span>
                {verdict?.status === "failed" && verdict.error ? (
                  <span
                    className="channel-model-test-error"
                    title={verdict.error}
                  >
                    {verdict.error}
                  </span>
                ) : null}
                <button
                  type="button"
                  className="unify-covered-toggle channel-model-test-run"
                  disabled={running || verdict?.status === "testing"}
                  onClick={() => void run([model.name])}
                >
                  {verdict?.status === "ok"
                    ? t("channels.testDialog.retest")
                    : t("channels.testDialog.test")}
                </button>
              </li>
            );
          })}
        </ul>
      )}
    </Dialog>
  );
}
