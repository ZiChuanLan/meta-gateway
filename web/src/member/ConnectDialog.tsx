import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Button, Dialog, Field, Loading } from "../components/ui";
import { accountRequest } from "../team/transport";
import { teamError, type TeamText } from "../team/text";
import type { UserKey } from "../team/types";
import { useI18n } from "../i18n";

type Client = "curl" | "python" | "ccswitch";
const CLIENTS: Array<{ id: Client; label: string }> = [
  { id: "curl", label: "cURL" },
  { id: "python", label: "Python" },
  { id: "ccswitch", label: "CC Switch" },
];

/**
 * "Connect": reveal one key and hand the user a ready-made client snippet.
 *
 * The plaintext is fetched on open (`/me/keys/{id}/reveal`), shown once in a
 * copy strip, and never cached: closing the dialog unmounts the query.
 * CC Switch import only *starts* the handoff — the page cannot see whether the
 * client accepted it, and the copy says so instead of claiming success.
 */
export function ConnectDialog({
  apiKey,
  token,
  initialModel,
  base,
  brandName,
  models,
  t,
  onClose,
  onCopy,
}: {
  apiKey: UserKey;
  /** A freshly minted token (create/rotate): skip the extra reveal round-trip. */
  token?: string;
  /** Model a "Connect →" action on the catalogue pinned, if any. */
  initialModel?: string;
  base: string;
  brandName: string;
  models: string[];
  t: TeamText;
  onClose: () => void;
  onCopy: (value: string, label?: string) => void;
}) {
  const { locale } = useI18n();
  const [client, setClient] = useState<Client>("curl");
  const [model, setModel] = useState(initialModel ?? models[0] ?? "");
  const reveal = useQuery({
    queryKey: ["user", "key", apiKey.id, "reveal"],
    queryFn: ({ signal }) =>
      accountRequest<{ token: string }>(`/me/keys/${apiKey.id}/reveal`, {
        method: "POST",
        signal,
      }),
    enabled: !token,
  });
  const secret = token ?? reveal.data?.token ?? "";
  const pending = !token && reveal.isPending;
  const failure = token ? null : reveal.error;
  const configuration =
    client === "python"
      ? `import requests\n\nresponse = requests.get(\n    ${JSON.stringify(base + "/models")},\n    headers={"Authorization": ${JSON.stringify("Bearer " + secret)}}\n)\nprint(response.json())`
      : client === "ccswitch"
        ? JSON.stringify(
            { name: brandName, endpoint: base, apiKey: secret, model },
            null,
            2,
          )
        : `curl ${JSON.stringify(base + "/models")} \\\n  -H ${JSON.stringify("Authorization: Bearer " + secret)}`;

  function importIntoClient() {
    if (!model) return;
    if (!confirm(t("importWarning"))) return;
    const params = new URLSearchParams({
      resource: "provider",
      app: "codex",
      name: brandName,
      endpoint: base,
      apiKey: secret,
      model,
    });
    try {
      location.assign("ccswitch://v1/import?" + params.toString());
      onCopy("", t("importStarted"));
    } catch {
      /* a browser that blocks the scheme leaves the dialog open */
    }
  }

  return (
    <Dialog
      title={`${t("connect")} · ${apiKey.name}`}
      onClose={onClose}
      busy={pending}
      actions={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("close")}
          </Button>
          {client === "ccswitch" ? (
            <Button disabled={!model || !secret} onClick={importIntoClient}>
              {t("importClient")}
            </Button>
          ) : (
            <Button
              disabled={!secret}
              onClick={() => onCopy(configuration, t("copied"))}
            >
              {t("copyConfig")}
            </Button>
          )}
        </>
      }
    >
      <p className="workspace-note" style={{ marginTop: 0 }}>
        {t("plaintextHint")}
      </p>
      {pending ? (
        <Loading />
      ) : failure ? (
        <div className="team-error" role="alert">
          {teamError(failure, locale)}
        </div>
      ) : (
        <>
          <div className="user-secret">
            <code>{secret}</code>
            <Button
              variant="secondary"
              onClick={() => onCopy(secret, t("copied"))}
            >
              {t("copy")}
            </Button>
          </div>
          <div className="user-base" style={{ marginTop: 12, marginBottom: 0 }}>
            <span className="workspace-caption">API BASE URL</span>
            <code>{base}</code>
          </div>
          <div className="tabs" style={{ marginTop: 18 }}>
            {CLIENTS.map((entry) => (
              <button
                key={entry.id}
                type="button"
                aria-selected={client === entry.id}
                onClick={() => setClient(entry.id)}
              >
                {entry.label}
              </button>
            ))}
          </div>
          {client === "ccswitch" && (
            <Field label={t("model")} className="form-grid-single">
              <select value={model} onChange={(e) => setModel(e.target.value)}>
                {models.map((name) => (
                  <option key={name}>{name}</option>
                ))}
              </select>
            </Field>
          )}
          <pre className="user-code">{configuration}</pre>
        </>
      )}
    </Dialog>
  );
}
