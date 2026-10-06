import { PriceFields, PricingRules } from "./PriceFields";
import { useState } from "react";
import type { RouteMember } from "../../api/types";
import {
  Button,
  Dialog,
  ErrorState,
  Field,
  InfoTip,
} from "../../components/ui";
import { useI18n } from "../../i18n";
import { memberRealName, serializeMemberMapping } from "../../lib/alias";
import {
  PriceTiersEditor,
  PriceWindowsEditor,
  encodeTiers,
  encodeWindows,
  parseTiers,
  parseWindows,
  validTiers,
  validWindows,
} from "./PricingEditors";

export function MemberDialog({
  value,
  channels,
  groups,
  pending,
  error,
  onClose,
  onSave,
}: {
  value: Partial<RouteMember>;
  channels: Array<{ id: number; name: string }>;
  groups: string[];
  pending: boolean;
  error: unknown;
  onClose: () => void;
  onSave: (value: Partial<RouteMember>) => void;
}) {
  const { t } = useI18n();
  const [pricesValid, setPricesValid] = useState(true);
  const [form, setForm] = useState(value);
  // The upstream model this member rewrites to. Unified aliases depend on it,
  // and without an editor the redirect was invisible in the UI.
  const [realName, setRealName] = useState(() => memberRealName(value));
  // Editing priority/weight makes the member independent of the connection
  // defaults (otherwise a later connection edit or model re-sync overwrites
  // the values). Keeping the checkbox off and saving without touching the
  // values preserves the previous state.
  const [valuesTouched, setValuesTouched] = useState(false);
  const markTouched = () => setValuesTouched(true);
  // This member's ladder and schedule. Arrays while editing, JSON text on the
  // wire — the conversion belongs to the editors, not to every keystroke here.
  const [tiersEdited, setTiersEdited] = useState(false);
  const [windowsEdited, setWindowsEdited] = useState(false);
  const [tiers, setTiers] = useState(() => parseTiers(value.price_tiers));
  const [windows, setWindows] = useState(() =>
    parseWindows(value.price_schedule),
  );
  return (
    <Dialog
      title={value.id ? t("routing.editMember") : t("routing.addMember")}
      onClose={onClose}
      busy={pending}
      actions={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            disabled={
              pending ||
              !form.channel_id ||
              !pricesValid ||
              !validTiers(tiers) ||
              !validWindows(windows)
            }
            onClick={() => {
              const next: Partial<RouteMember> = {
                ...form,
                ...(tiersEdited ? { price_tiers: encodeTiers(tiers) } : {}),
                ...(windowsEdited
                  ? { price_schedule: encodeWindows(windows) }
                  : {}),
                ...(valuesTouched &&
                (form.priority !== value.priority ||
                  form.weight !== value.weight)
                  ? { manual_override: true }
                  : {}),
              };
              if (!value.id) {
                onSave(next);
                return;
              }
              // Send intent, not the stale snapshot loaded when the editor opened.
              onSave(
                Object.fromEntries(
                  Object.entries(next).filter(
                    ([key, item]) =>
                      key === "id" ||
                      JSON.stringify(item) !==
                        JSON.stringify(value[key as keyof RouteMember]),
                  ),
                ) as Partial<RouteMember>,
              );
            }}
          >
            {pending ? t("common.working") : t("common.save")}
          </Button>
        </>
      }
    >
      {!value.id ? (
        <Field label={t("common.channel")}>
          <select
            value={form.channel_id ?? ""}
            onChange={(event) =>
              setForm({
                ...form,
                channel_id: Number(event.target.value) || undefined,
              })
            }
          >
            <option value="">{t("common.select")}</option>
            {channels.map((channel) => (
              <option key={channel.id} value={channel.id}>
                {channel.name}
              </option>
            ))}
          </select>
        </Field>
      ) : null}
      <div className="ops-panel-context">
        <span>{t("routing.memberDialogIntro")}</span>
      </div>
      <div className="form-grid">
        <Field label={t("routing.priorityLabel")}>
          <input
            type="number"
            value={form.priority ?? 0}
            onChange={(event) => {
              markTouched();
              setForm({ ...form, priority: Number(event.target.value) });
            }}
          />
          <InfoTip label={t("routing.priorityHint")} />
        </Field>
        <Field label={t("routing.weightLabel")}>
          <input
            type="number"
            min={0}
            value={form.weight ?? 100}
            onChange={(event) => {
              markTouched();
              setForm({ ...form, weight: Number(event.target.value) });
            }}
          />
          <InfoTip label={t("routing.weightHint")} />
        </Field>
        <PriceFields
          value={form}
          onChange={(partial) => setForm({ ...form, ...partial })}
          onValidityChange={setPricesValid}
          disabled={pending}
        />
        <PricingRules />
        <PriceTiersEditor
          value={tiers}
          onChange={(next) => {
            setTiersEdited(true);
            setTiers(next);
          }}
          disabled={pending}
        />
        <PriceWindowsEditor
          value={windows}
          onChange={(next) => {
            setWindowsEdited(true);
            setWindows(next);
          }}
          disabled={pending}
        />
      </div>
      <Field label={t("routing.memberGroupLabel")}>
        <select
          value={form.group_name || "default"}
          onChange={(event) =>
            setForm({ ...form, group_name: event.target.value || "default" })
          }
        >
          {[...new Set(["default", ...groups])].map((group) => (
            <option key={group} value={group}>
              {group === "default" ? t("routing.groupDefault") : group}
            </option>
          ))}
        </select>
        <InfoTip label={t("routing.memberGroupHint")} />
      </Field>
      <Field label={t("routing.memberRealName")}>
        <input
          value={realName}
          placeholder={t("routing.memberRealNamePlaceholder")}
          onChange={(event) => {
            const next = event.target.value;
            setRealName(next);
            setForm({ ...form, mapping_json: serializeMemberMapping(next) });
          }}
        />
        <InfoTip label={t("routing.memberRealNameHint")} />
      </Field>
      <label className="check">
        <input
          type="checkbox"
          checked={form.enabled ?? true}
          onChange={(event) =>
            setForm({ ...form, enabled: event.target.checked })
          }
        />
        <span>
          <strong>{t("routing.enabledLabel")}</strong>
          <InfoTip label={t("routing.enabledHint")} />
        </span>
      </label>
      {error ? <ErrorState error={error} /> : null}
    </Dialog>
  );
}
