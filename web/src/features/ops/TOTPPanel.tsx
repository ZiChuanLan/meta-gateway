import { useQuery, useQueryClient } from "@tanstack/react-query"
import { useState } from "react"
import { api } from "../../api/client"
import { useI18n } from "../../i18n"
import { useSession } from "../../session"
import { Button, Panel, StatusBadge } from "../../components/ui"

export // Admin TOTP 2FA panel: setup (show secret + otpauth URI), enable with a
// code, and disable with a current code.
function TOTPPanel() {
  const { client } = useSession();
  const service = api(client!);
  const { t } = useI18n();
  const queryClient = useQueryClient();
  const [phase, setPhase] = useState<"idle" | "setup">("idle");
  const [setupData, setSetupData] = useState<{ secret: string; otpauth_uri: string } | null>(null);
  const [code, setCode] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  const status = useQuery({
    queryKey: ["totp-status"],
    queryFn: ({ signal }) => service.totpStatus(signal),
    refetchInterval: 30_000,
  });
  const enabled = status.data?.enabled ?? false;
  const refresh = () => {
    queryClient.invalidateQueries({ queryKey: ["totp-status"] });
    setPhase("idle");
    setSetupData(null);
    setCode("");
    setError("");
  };
  const run = async (fn: () => Promise<unknown>, onSuccess?: () => void) => {
    setBusy(true);
    setError("");
    try {
      await fn();
      onSuccess?.();
    } catch (err) {
      setError(err instanceof Error ? err.message : t("common.failed"));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Panel
      className="runtime-card runtime-tool-totp"
      id="runtime-totp"
    >
      <div className="panel-header">
        <strong>{t("ops.runtime.totpTitle")}</strong>
        {enabled ? (
          <StatusBadge value="success" />
        ) : (
          <span className="runtime-setting-value muted">
            {t("ops.runtime.totpDisabled")}
          </span>
        )}
      </div>
      {!enabled && phase === "idle" ? (
        <Button
          variant="secondary"
          disabled={busy}
          onClick={() =>
            void run(async () => {
              const res = await service.totpSetup();
              setSetupData(res);
              setPhase("setup");
            })
          }
        >
          {t("ops.runtime.totpSetup")}
        </Button>
      ) : null}
      {!enabled && phase === "setup" && setupData ? (
        <div className="totp-setup">
          <p className="muted" style={{ fontSize: 12 }}>
            {t("ops.runtime.totpSetupHint")}
          </p>
          <div className="totp-secret-row mono">
            <code>{setupData.secret}</code>
            <button
              type="button"
              className="redemption-copy"
              onClick={() => void navigator.clipboard.writeText(setupData.secret)}
            >
              {t("keys.redemptionCopy")}
            </button>
          </div>
          <a
            className="totp-uri-link"
            href={setupData.otpauth_uri}
            target="_blank"
            rel="noopener noreferrer"
          >
            {t("ops.runtime.totpOpenApp")}
          </a>
          <input
            type="text"
            inputMode="numeric"
            pattern="[0-9]{6}"
            maxLength={6}
            value={code}
            onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
            placeholder="123456"
            disabled={busy}
            style={{ width: 160 }}
          />
          <Button
            disabled={busy || code.length !== 6}
            onClick={() => void run(() => service.totpEnable(code), refresh)}
          >
            {t("ops.runtime.totpEnable")}
          </Button>
        </div>
      ) : null}
      {enabled ? (
        <div className="totp-setup">
          <p className="muted" style={{ fontSize: 12 }}>
            {t("ops.runtime.totpDisableHint")}
          </p>
          <input
            type="text"
            inputMode="numeric"
            pattern="[0-9]{6}"
            maxLength={6}
            value={code}
            onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
            placeholder="123456"
            disabled={busy}
            style={{ width: 160 }}
          />
          <Button
            variant="danger"
            disabled={busy || code.length !== 6}
            onClick={() => void run(() => service.totpDisable(code), refresh)}
          >
            {t("ops.runtime.totpDisable")}
          </Button>
        </div>
      ) : null}
      {error ? <div className="inline-error">{error}</div> : null}
    </Panel>
  );
}
