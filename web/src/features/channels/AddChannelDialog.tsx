import { Cable, ChevronDown } from "lucide-react";
import { useState } from "react";
import { api } from "../../api/client";
import { SearchableSelect } from "../../components/SearchableSelect";
import {
  Button,
  Dialog,
  ErrorState,
  Field,
  InfoTip,
} from "../../components/ui";
import { useI18n } from "../../i18n";
import { PROVIDER_BASE_URLS } from "../../connectionTypes";
import { useSession } from "../../session";
import { TYPE_OPTIONS } from "./helpers";
import { TYPE_GROUPS, type CreateConnectionInput } from "./helpers";

export function AddChannelDialog({
  pending,
  error,
  onClose,
  onSave,
}: {
  pending: boolean;
  error: unknown;
  onClose: () => void;
  onSave: (value: CreateConnectionInput, options: { verify: boolean }) => void;
}) {
  const { t } = useI18n();
  const { client } = useSession();
  const service = api(client!);
  const [name, setName] = useState("");
  const [baseUrl, setBaseUrl] = useState("");
  const [secret, setSecret] = useState("");
  const [typeHint, setTypeHint] = useState("openai-compatible");
  const [groupName, setGroupName] = useState("default");
  const [showAdvanced, setShowAdvanced] = useState(false);
  const canSubmit = Boolean(baseUrl.trim() && secret.trim());

  return (
    <Dialog
      title={t("channels.add")}
      onClose={onClose}
      actions={
        <>
          <Button variant="secondary" onClick={onClose} disabled={pending}>
            {t("common.cancel")}
          </Button>
          <Button
            icon={<Cable size={16} />}
            disabled={pending || !canSubmit}
            onClick={() =>
              onSave(
                {
                  name,
                  base_url: baseUrl,
                  secret,
                  type_hint: typeHint,
                  group_name: groupName,
                },
                { verify: true },
              )
            }
          >
            {pending ? t("common.working") : t("channels.saveAndVerify")}
          </Button>
        </>
      }
    >
      <div className="ops-panel-context">
        <span>{t("channels.addHint")}</span>
      </div>
      <div className="form-grid form-grid-single">
        <Field label={t("common.type")}>
          <SearchableSelect
            options={TYPE_OPTIONS}
            groups={TYPE_GROUPS}
            value={typeHint}
            onChange={(next) => {
              const provider = next ?? "openai-compatible";
              // Auto-fill the provider default base URL when the field is
              // empty or still holds the previous provider's default.
              setBaseUrl((current) => {
                const currentTrimmed = current.trim().replace(/\/+$/, "");
                const previousDefault = PROVIDER_BASE_URLS[typeHint] ?? "";
                if (
                  currentTrimmed === "" ||
                  (previousDefault &&
                    currentTrimmed === previousDefault.replace(/\/+$/, ""))
                ) {
                  return PROVIDER_BASE_URLS[provider] ?? "";
                }
                return current;
              });
              setTypeHint(provider);
            }}
            disabled={pending}
            allowCustom
            placeholder={t("common.type")}
          />
        </Field>
        <Field label={t("common.name")}>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder={t("channels.namePlaceholder")}
            disabled={pending}
          />
        </Field>
        <Field label={t("common.baseUrl")} hint={t("channels.baseUrlHint")}>
          <input
            type="url"
            required
            value={baseUrl}
            onChange={(e) => setBaseUrl(e.target.value)}
            onBlur={() => {
              // AAH-style auto-detection: sniff the platform from the URL so
              // the operator does not have to pick the type manually.
              const url = baseUrl.trim();
              if (!url) return;
              service
                .detectSiteType(url)
                .then((detected) => {
                  if (
                    detected.family &&
                    TYPE_OPTIONS.some((o) => o.value === detected.family)
                  ) {
                    setTypeHint(detected.family);
                  }
                })
                .catch(() => undefined);
            }}
            placeholder="https://api.example.com"
            disabled={pending}
          />
        </Field>
        <Field label={t("channels.group")} hint={t("channels.groupHint")}>
          <input
            value={groupName}
            onChange={(event) => setGroupName(event.target.value)}
            disabled={pending}
          />
        </Field>
        <Field label={t("common.secret")}>
          <input
            type="password"
            autoComplete="new-password"
            required
            value={secret}
            onChange={(e) => setSecret(e.target.value)}
            disabled={pending}
          />
        </Field>
      </div>
      <button
        type="button"
        className={`advanced-toggle${showAdvanced ? " is-open" : ""}`}
        onClick={() => setShowAdvanced((value) => !value)}
      >
        <ChevronDown size={13} />
        {showAdvanced ? t("channels.hideAdvanced") : t("channels.showAdvanced")}
      </button>
      {showAdvanced ? (
        <div style={{ marginTop: 4 }}>
          <Button
            variant="secondary"
            disabled={pending || !canSubmit}
            onClick={() =>
              onSave(
                {
                  name,
                  base_url: baseUrl,
                  secret,
                  type_hint: typeHint,
                  group_name: groupName,
                },
                { verify: false },
              )
            }
          >
            {t("channels.saveOnly")}
          </Button>
          <InfoTip label={t("channels.saveOnlyHint")} />
        </div>
      ) : null}

      {error ? <ErrorState error={error} /> : null}
    </Dialog>
  );
}
