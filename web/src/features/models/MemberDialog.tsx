import { useState } from "react";
import type { RouteMember } from "../../api/types";
import { SearchableSelect } from "../../components/SearchableSelect";
import { Button, Dialog, ErrorState, Field, InfoTip } from "../../components/ui";
import { useI18n } from "../../i18n";
import { memberRealName, serializeMemberMapping } from "../../lib/alias";

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
  channels: Array<{ id: number; name: string; models_csv?: string }>;
  groups: string[];
  pending: boolean;
  error: unknown;
  onClose: () => void;
  onSave: (value: Partial<RouteMember>) => void;
}) {
  const { t } = useI18n();
  const [form, setForm] = useState(value);
  // The upstream model this member rewrites to. Unified aliases depend on it,
  // and without an editor the redirect was invisible in the UI.
  const [realName, setRealName] = useState(() => memberRealName(value));
  // The selected channel's own model list, so the alias target can be picked
  // instead of copied by hand. Wildcards are route matchers, not model names.
  // Deduped because a hand-imported models_csv can repeat names.
  const upstreamChoices = [
    ...new Set(
      (channels.find((channel) => channel.id === form.channel_id)?.models_csv ?? "")
        .split(",")
        .map((name) => name.trim())
        .filter((name) => name && !/[*?]/.test(name)),
    ),
  ];
  // Editing priority/weight makes the member independent of the connection
  // defaults (otherwise a later connection edit or model re-sync overwrites
  // the values). Keeping the checkbox off and saving without touching the
  // values preserves the previous state.
  const [valuesTouched, setValuesTouched] = useState(false);
  const markTouched = () => setValuesTouched(true);
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
            disabled={pending || !form.channel_id}
            onClick={() => {
              const next: Partial<RouteMember> = {
                ...form,
                ...(valuesTouched &&
                (form.priority !== value.priority || form.weight !== value.weight)
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
                      JSON.stringify(item) !== JSON.stringify(value[key as keyof RouteMember]),
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
            onChange={(event) => {
              // The previous channel's alias target says nothing about this
              // one: keeping it would silently send the wrong upstream model
              // (or a name this channel never advertised).
              setForm({
                ...form,
                channel_id: Number(event.target.value) || undefined,
                mapping_json: "",
              });
              setRealName("");
            }}
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
        <p className="field-hint">{t("billing.priceLocation")}</p>
      </div>
      <Field label={t("routing.memberGroupLabel")}>
        <select
          value={form.group_name || "default"}
          onChange={(event) => setForm({ ...form, group_name: event.target.value || "default" })}
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
        <SearchableSelect
          // Remount on channel change: the picker keeps its own "custom entry"
          // state, and a stale one would outlive the options it belonged to.
          key={form.channel_id ?? "none"}
          ariaLabel={t("routing.memberRealName")}
          // A blank target is a real choice here, so it is an option rather
          // than an implied "nothing selected".
          options={[
            { value: "", label: t("routing.memberRealNameBlank") },
            ...upstreamChoices.map((model) => ({ value: model, label: model })),
          ]}
          value={realName}
          // `placeholder` also labels the dropdown's search box, so it must be a
          // short call to action, not the sentence describing the blank option.
          placeholder={t("routing.memberRealNameSearch")}
          customLabel={t("routing.memberRealNameCustom")}
          customPlaceholder={t("routing.memberRealNamePlaceholder")}
          allowCustom
          onChange={(next) => {
            // The marker means "I am about to type", not "I chose nothing".
            // Mapping it to a blank target deleted an already stored alias, so
            // a stray click on the custom row wiped the member's redirect.
            if (next === "__custom__") return;
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
          onChange={(event) => setForm({ ...form, enabled: event.target.checked })}
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
