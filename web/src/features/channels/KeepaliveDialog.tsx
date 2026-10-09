import { BellRing, ChevronRight, Play, RefreshCw, Settings2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { api } from "../../api/client";
import type { KeepaliveTarget, SiteKeepaliveInput } from "../../api/types";
import { Button, Dialog, Empty, ErrorState, Field, InfoTip, Loading } from "../../components/ui";
import { useAdminMutation } from "../../hooks/useAdminMutation";
import { useI18n } from "../../i18n";
import { useSession } from "../../session";

/**
 * The keepalive workspace: every channel's resolved window, and the two things
 * an operator can do about it.
 *
 * It is deliberately not a scheduler UI. A site bans "no call for N days", so
 * what matters is the idle age of each account against its own window; a channel
 * that already has traffic is never listed as "due", and the page says so
 * instead of implying a timer is running.
 *
 * Every site's rows are on screen at once (no folding): the question this page
 * answers — "which account is about to be banned" — is answered by scanning the
 * 剩余 column, and a folded row would hide exactly the number that answers it.
 */
export function KeepaliveDialog({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const { client } = useSession();
  // Which site's settings are open, and which of its channels the send button
  // belongs to: the settings are the site's, the call is one channel's.
  const [editing, setEditing] = useState<{ siteId: number; row: KeepaliveTarget } | null>(null);
  const [notice, setNotice] = useState<string | null>(null);

  const status = useQuery({
    queryKey: ["keepalive", client],
    queryFn: ({ signal }) => api(client!).keepalive(signal),
  });
  const events = useQuery({
    queryKey: ["keepalive-events", client],
    queryFn: ({ signal }) => api(client!).keepaliveEvents(20, signal),
  });

  const runRound = useAdminMutation({
    mutationFn: () => api(client!).keepaliveRun(),
    invalidateKeys: [["keepalive"], ["keepalive-events"]],
    onSuccess: (round) => {
      setNotice(
        t("channels.keepalive.roundDone", {
          sent: String(round.Sent),
          failed: String(round.Failed),
          notReady: String(round.NotReady),
        }),
      );
    },
  });

  const sendNow = useAdminMutation({
    mutationFn: (channelId: number) => api(client!).keepaliveSend(channelId),
    invalidateKeys: [["keepalive"], ["keepalive-events"]],
    onSuccess: (event) =>
      setNotice(
        event.ok
          ? t("channels.keepalive.sentOk", { model: event.model })
          : t("channels.keepalive.sentFailed", { error: event.error ?? String(event.status_code) }),
      ),
  });

  const targets = status.data?.targets ?? [];

  const toggleSettings = (siteId: number, row: KeepaliveTarget) =>
    setEditing((current) =>
      current?.siteId === siteId && current.row.channel_id === row.channel_id
        ? null
        : { siteId, row },
    );

  // The settings form opens above the list, so it must be brought into view when
  // the row that opened it sits at the bottom of a long dialog.
  const settingsRef = useRef<HTMLDivElement | null>(null);
  useEffect(() => {
    if (editing) settingsRef.current?.scrollIntoView({ block: "nearest" });
  }, [editing]);

  return (
    <Dialog title={t("channels.keepalive.title")} onClose={onClose} busy={runRound.isPending}>
      {/* A stable marker for the width rule: the table sizes the dialog, and the
          marker must not depend on what happens to be rendered inside it. */}
      <div className="keepalive-workspace">
        <p className="muted panel-lede">
          {t("channels.keepalive.intro")}
          <InfoTip label={t("channels.keepalive.introTip")} />
        </p>

        {status.isPending ? <Loading /> : null}
        {status.error ? <ErrorState error={status.error} /> : null}

        {status.data ? (
          <>
            <div className="toolbar keepalive-toolbar">
              <Button
                variant="secondary"
                icon={<Play size={15} />}
                disabled={runRound.isPending}
                onClick={() => {
                  setNotice(null);
                  runRound.mutate(undefined);
                }}
              >
                {t("channels.keepalive.runNow")}
              </Button>
              <Button
                variant="quiet"
                icon={<RefreshCw size={15} className={status.isFetching ? "spin" : ""} />}
                onClick={() => void status.refetch()}
              >
                {t("channels.keepalive.refresh")}
              </Button>
              <span className="muted keepalive-clock">
                {t("channels.keepalive.serverTime")}: {formatTime(status.data.now)}
              </span>
            </div>

            {notice ? <p className="keepalive-notice">{notice}</p> : null}

            {editing ? (
              <div className="keepalive-settings" ref={settingsRef}>
                <header className="keepalive-settings-head">
                  <h3>
                    {t("channels.keepalive.settingsFor", {
                      site: editing.row.site_name || `#${editing.siteId}`,
                    })}
                  </h3>
                </header>
                <SiteSettings
                  siteId={editing.siteId}
                  rows={targets.filter((target) => target.site_id === editing.siteId)}
                  row={editing.row}
                  sending={sendNow.isPending}
                  onSendNow={() => {
                    setNotice(null);
                    sendNow.mutate(editing.row.channel_id);
                  }}
                  onDone={() => {
                    setEditing(null);
                    void status.refetch();
                  }}
                  onCancel={() => setEditing(null)}
                />
              </div>
            ) : null}

            {targets.length === 0 ? (
              <Empty>{t("channels.keepalive.empty")}</Empty>
            ) : (
              <div className="keepalive-list">
                <table className="keepalive-table">
                  <thead>
                    <tr>
                      <th>{t("channels.keepalive.columnSite")}</th>
                      <th>{t("channels.keepalive.columnChannel")}</th>
                      <th>{t("channels.keepalive.columnWindow")}</th>
                      <th>{t("channels.keepalive.columnIdle")}</th>
                      <th>{t("channels.keepalive.columnRemaining")}</th>
                      <th>{t("channels.keepalive.columnState")}</th>
                      <th />
                    </tr>
                  </thead>
                  <tbody>
                    {targets.map((row) => (
                      <tr
                        key={row.channel_id}
                        className={
                          editing?.row.channel_id === row.channel_id ? "is-editing" : undefined
                        }
                      >
                        <td className="keepalive-site-cell">
                          <span className="keepalive-site-label">
                            {row.site_name || `#${row.site_id}`}
                          </span>
                          {/* Worth its space only where the site forbids probing:
                              the default needs no badge. */}
                          {row.call_policy === "real_calls_only" ? (
                            <span className={policyClass(row)}>{policyLabel(row, t)}</span>
                          ) : null}
                        </td>
                        <td>
                          <span className="keepalive-channel">{row.channel_name}</span>
                          <span className="muted keepalive-model">
                            {row.model || t("channels.keepalive.noModel")}
                          </span>
                        </td>
                        <td>
                          {row.config.idle_days > 0
                            ? t("channels.keepalive.windowValue", {
                                days: String(row.config.idle_days),
                                margin: String(row.config.safety_margin_days),
                              })
                            : t("channels.keepalive.noWindow")}
                        </td>
                        <td>
                          {row.never_called
                            ? t("channels.keepalive.neverCalled")
                            : days(row.idle_days)}
                        </td>
                        <td className={row.remaining_days <= 3 ? "keepalive-urgent" : undefined}>
                          {row.config.idle_days > 0 ? days(row.remaining_days) : "—"}
                        </td>
                        <td>
                          <span className={stateClass(row)}>{stateLabel(row, t)}</span>
                          {row.sends_today > 0 ? (
                            <span className="muted keepalive-today">
                              {t("channels.keepalive.sentToday", {
                                count: String(row.sends_today),
                              })}
                            </span>
                          ) : null}
                        </td>
                        <td className="keepalive-actions">
                          <Button
                            variant="quiet"
                            icon={<Settings2 size={14} />}
                            onClick={() => toggleSettings(row.site_id, row)}
                          >
                            {t("channels.keepalive.configureSite")}
                          </Button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </div>
            )}

            <details className="keepalive-footprint">
              <summary>
                <ChevronRight
                  size={14}
                  className="keepalive-footprint-chevron"
                  aria-hidden="true"
                />
                <span>{t("channels.keepalive.footprint")}</span>
                {events.data && events.data.length > 0 ? (
                  <span className="keepalive-footprint-count">
                    {t("channels.keepalive.footprintCount", {
                      count: String(events.data.length),
                    })}
                  </span>
                ) : null}
              </summary>
              <div className="keepalive-footprint-body">
                <p className="muted panel-lede">{t("channels.keepalive.footprintHint")}</p>
                {events.data && events.data.length > 0 ? (
                  <ul className="keepalive-events">
                    {events.data.map((event) => (
                      <li key={event.id} className={event.ok ? "" : "is-failed"}>
                        <span className="keepalive-event-time">{formatTime(event.created_at)}</span>
                        <span className="keepalive-event-what">
                          {event.site_name} / {event.channel_name} · {event.model}
                        </span>
                        <span className="muted keepalive-event-why">
                          {event.form === "real"
                            ? t("channels.keepalive.formReal")
                            : t("channels.keepalive.formMinimal")}{" "}
                          · {event.reason}
                          {event.ok ? "" : ` · ${event.error ?? event.status_code}`}
                        </span>
                      </li>
                    ))}
                  </ul>
                ) : (
                  <p className="muted">{t("channels.keepalive.noEvents")}</p>
                )}
              </div>
            </details>
          </>
        ) : null}
      </div>
    </Dialog>
  );
}

/**
 * The per-site editor, opened from a channel row's 站点设置.
 *
 * The window is the site's rule, so it is edited once for the whole site rather
 * than per channel — a channel only overrides whether it participates. The model
 * is the site's too, and the one thing the operator does not have to fill in:
 * left empty, the channel is called on the first model it advertised. 立即保活
 * sits here with it, because a call is made *with* these settings — and it is
 * disabled while the form is dirty, so nobody sends with settings they can still
 * see are unsaved.
 */
function SiteSettings({
  siteId,
  rows,
  row,
  sending,
  onSendNow,
  onDone,
  onCancel,
}: {
  siteId: number;
  rows: KeepaliveTarget[];
  row: KeepaliveTarget;
  sending: boolean;
  onSendNow: () => void;
  onDone: () => void;
  onCancel: () => void;
}) {
  const { t } = useI18n();
  const { client } = useSession();
  const [form, setForm] = useState<SiteKeepaliveInput>({
    call_policy: row.call_policy || "allow_probe",
    keepalive_enabled: row.config.enabled,
    keepalive_idle_days: row.config.idle_days,
    keepalive_safety_margin_days: row.config.safety_margin_days,
    // The site's own setting, not the resolved model: empty means "automatic",
    // and writing the resolved one back would pin it forever.
    keepalive_model: row.site_model ?? "",
    keepalive_prompt: row.config.prompt ?? "",
    keepalive_max_tokens: row.config.max_tokens ?? 0,
    keepalive_daily_cap: row.config.daily_cap ?? 0,
    keepalive_quiet_hours: row.config.quiet_hours ?? "",
  });
  const initial = useRef(form);
  const dirty = !sameSettings(form, initial.current);

  // Everything the site's channels advertised, so the field offers real names
  // instead of an empty box; the operator can still type one that is not here.
  const models = [...new Set(rows.flatMap((target) => target.candidates ?? []))].sort((a, b) =>
    a.localeCompare(b),
  );
  const autoModel = rows.map((target) => target.candidates?.[0]).find(Boolean) ?? "";
  const listId = `keepalive-models-${siteId}`;

  const save = useAdminMutation({
    mutationFn: (input: SiteKeepaliveInput) => api(client!).keepaliveSaveSite(siteId, input),
    invalidateKeys: [["keepalive"]],
    onSuccess: onDone,
  });

  return (
    <form
      className="form-grid keepalive-form"
      onSubmit={(event) => {
        event.preventDefault();
        save.mutate(form);
      }}
    >
      <Field label={t("channels.keepalive.fieldEnabled")}>
        <label className="check">
          <input
            type="checkbox"
            checked={form.keepalive_enabled}
            onChange={(e) => setForm((f) => ({ ...f, keepalive_enabled: e.target.checked }))}
          />
          <span>{t("channels.keepalive.fieldEnabledHint")}</span>
        </label>
      </Field>
      <Field label={t("channels.keepalive.fieldPolicy")}>
        <select
          value={form.call_policy}
          onChange={(e) => setForm((f) => ({ ...f, call_policy: e.target.value }))}
        >
          <option value="allow_probe">{t("channels.keepalive.policyAllowProbe")}</option>
          <option value="real_calls_only">{t("channels.keepalive.policyRealCallsOnly")}</option>
        </select>
      </Field>
      <Field label={t("channels.keepalive.fieldIdleDays")}>
        <input
          type="number"
          min={0}
          max={3650}
          value={form.keepalive_idle_days}
          onChange={(e) =>
            setForm((f) => ({ ...f, keepalive_idle_days: Number(e.target.value) || 0 }))
          }
        />
      </Field>
      <Field label={t("channels.keepalive.fieldMargin")}>
        <input
          type="number"
          min={0}
          max={90}
          value={form.keepalive_safety_margin_days}
          onChange={(e) =>
            setForm((f) => ({ ...f, keepalive_safety_margin_days: Number(e.target.value) || 0 }))
          }
        />
      </Field>
      <Field
        label={t("channels.keepalive.fieldModel")}
        hint={
          autoModel
            ? t("channels.keepalive.fieldModelAuto", { model: autoModel })
            : t("channels.keepalive.fieldModelNone")
        }
      >
        <input
          type="text"
          list={listId}
          placeholder={autoModel || undefined}
          value={form.keepalive_model}
          onChange={(e) => setForm((f) => ({ ...f, keepalive_model: e.target.value }))}
        />
        <datalist id={listId}>
          {models.map((model) => (
            <option key={model} value={model} />
          ))}
        </datalist>
      </Field>
      <Field label={t("channels.keepalive.fieldMaxTokens")}>
        <input
          type="number"
          min={0}
          max={32000}
          value={form.keepalive_max_tokens}
          onChange={(e) =>
            setForm((f) => ({ ...f, keepalive_max_tokens: Number(e.target.value) || 0 }))
          }
        />
      </Field>
      <Field label={t("channels.keepalive.fieldDailyCap")}>
        <input
          type="number"
          min={0}
          max={24}
          value={form.keepalive_daily_cap}
          onChange={(e) =>
            setForm((f) => ({ ...f, keepalive_daily_cap: Number(e.target.value) || 0 }))
          }
        />
      </Field>
      <Field
        label={t("channels.keepalive.fieldQuietHours")}
        hint={t("channels.keepalive.fieldQuietHoursHint")}
      >
        <input
          type="text"
          placeholder="22:00-07:00"
          value={form.keepalive_quiet_hours}
          onChange={(e) => setForm((f) => ({ ...f, keepalive_quiet_hours: e.target.value }))}
        />
      </Field>
      <Field label={t("channels.keepalive.fieldPrompt")}>
        <input
          type="text"
          placeholder={t("channels.keepalive.fieldPromptPlaceholder")}
          value={form.keepalive_prompt}
          onChange={(e) => setForm((f) => ({ ...f, keepalive_prompt: e.target.value }))}
        />
      </Field>
      <div className="wide toolbar keepalive-form-actions">
        <Button type="submit" disabled={save.isPending}>
          {t("common.save")}
        </Button>
        <Button type="button" variant="quiet" onClick={onCancel}>
          {t("common.cancel")}
        </Button>
        {save.error ? <span className="inline-error">{String(save.error)}</span> : null}
        <span className="muted keepalive-send-hint">
          {dirty
            ? t("channels.keepalive.sendNowDirty")
            : t("channels.keepalive.sendNowHint", {
                channel: row.channel_name,
                model: form.keepalive_model || autoModel || "—",
              })}
        </span>
        <Button
          type="button"
          variant="secondary"
          className="keepalive-send"
          icon={<BellRing size={14} />}
          disabled={dirty || sending || Boolean(row.skip_reason)}
          onClick={onSendNow}
        >
          {t("channels.keepalive.sendNow")}
        </Button>
      </div>
    </form>
  );
}

/** Whether the operator still has edits the server has not seen. */
function sameSettings(a: SiteKeepaliveInput, b: SiteKeepaliveInput): boolean {
  return (
    a.call_policy === b.call_policy &&
    a.keepalive_enabled === b.keepalive_enabled &&
    a.keepalive_idle_days === b.keepalive_idle_days &&
    a.keepalive_safety_margin_days === b.keepalive_safety_margin_days &&
    a.keepalive_model === b.keepalive_model &&
    a.keepalive_prompt === b.keepalive_prompt &&
    a.keepalive_max_tokens === b.keepalive_max_tokens &&
    a.keepalive_daily_cap === b.keepalive_daily_cap &&
    a.keepalive_quiet_hours === b.keepalive_quiet_hours
  );
}

function stateLabel(
  row: KeepaliveTarget,
  t: (key: string, vars?: Record<string, string>) => string,
): string {
  if (row.skip_reason) return t("channels.keepalive.stateNoModel");
  if (!row.config.enabled || row.config.idle_days <= 0) return t("channels.keepalive.stateOff");
  if (row.ready) return t("channels.keepalive.stateDue");
  return t("channels.keepalive.stateWaiting");
}

function stateClass(row: KeepaliveTarget): string {
  if (row.skip_reason) return "keepalive-state is-warn";
  if (!row.config.enabled || row.config.idle_days <= 0) return "keepalive-state is-off";
  if (row.ready) return "keepalive-state is-due";
  return "keepalive-state";
}

function policyLabel(
  row: KeepaliveTarget,
  t: (key: string, vars?: Record<string, string>) => string,
): string {
  return row.call_policy === "real_calls_only"
    ? t("channels.keepalive.policyRealCallsOnly")
    : t("channels.keepalive.policyAllowProbe");
}

function policyClass(row: KeepaliveTarget): string {
  return row.call_policy === "real_calls_only" ? "keepalive-policy is-real" : "keepalive-policy";
}

function days(value: number): string {
  if (!Number.isFinite(value)) return "—";
  if (Math.abs(value) < 0.05) return "0 d";
  return `${value.toFixed(1)} d`;
}

function formatTime(value: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString();
}
