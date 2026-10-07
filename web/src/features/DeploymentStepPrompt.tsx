import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { Button, Dialog } from "../components/ui";

/** The one-time step, as one line an operator can paste in the deploy directory. */
const COMMAND =
  "git pull --ff-only && docker compose pull meta-gateway && docker compose up -d --no-build --force-recreate meta-gateway";

/**
 * The one-time deployment step, shown after an upgrade that went through the
 * console.
 *
 * An upgrade replaces the image; it never re-reads `docker-compose.yml`. So the
 * container keeps the environment it was created with: variables added to `.env`
 * since (and, before v4.1.0, the compose-updater sidecar itself) are still inert,
 * and nothing in the running gateway would ever say so. The server decides from
 * the container's own environment whether the file has been applied, which means
 * this notice retires itself — dismissing it only silences it for this browser
 * session, and it comes back in a new session while the step is still undone.
 */
export function DeploymentStepPrompt({ enabled }: { enabled: boolean }) {
  const { client, role } = useSession();
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);
  const [dismissed, setDismissed] = useState(() => {
    try {
      return sessionStorage.getItem("deployment-step-dismissed") === "1";
    } catch {
      return false;
    }
  });
  const owner = role === null || role === "owner";
  const status = useQuery({
    queryKey: ["self-update"],
    queryFn: ({ signal }) =>
      client!.get<{ deployment_step?: string }>("/admin/self-update", signal),
    enabled: Boolean(client) && owner && enabled && !dismissed,
  });
  const close = () => {
    setDismissed(true);
    try {
      sessionStorage.setItem("deployment-step-dismissed", "1");
    } catch {
      /* optional */
    }
  };
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(COMMAND);
      setCopied(true);
    } catch {
      /* Clipboard unavailable: the command is on screen to select. */
    }
  };
  // Every gate is checked here, not only in `enabled`: disabling a query stops
  // the fetch but leaves cached data, and the prompt must disappear the moment it
  // is dismissed or the earlier prompt has not resolved yet.
  if (!enabled || !owner || dismissed || !status.data?.deployment_step) return null;
  return (
    <Dialog title={t("deploy.stepTitle")} onClose={close} busy={false}>
      <p>{t("deploy.stepWhy")}</p>
      <pre className="update-notes">{COMMAND}</pre>
      <p className="field-hint">{t("deploy.stepNote")}</p>
      <div className="update-dialog-actions">
        <Button variant="secondary" onClick={copy}>
          {copied ? t("deploy.stepCopied") : t("deploy.stepCopy")}
        </Button>
        <Button variant="quiet" onClick={close}>
          {t("deploy.stepLater")}
        </Button>
      </div>
    </Dialog>
  );
}
