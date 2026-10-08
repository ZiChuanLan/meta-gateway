import { BellRing, Play, RefreshCw, Settings2 } from "lucide-react";
import { useQuery } from "@tanstack/react-query";
import { useState } from "react";
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
 */
export function KeepaliveDialog({ onClose }: { onClose: () => void }) {
  const { t } = useI18n();
  const { client } = useSession();
  const [editingSite, setEditingSite] = useState<number | null>(null);
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
  const grouped = groupBySite(targets);

  return (
    <Dialog title={t("channels.keepalive.title")} onClose={onClose} busy={runRound.isPending}>
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

          {targets.length === 0 ? (
            <Empty>{t("channels.keepalive.empty")}</Empty>
          ) : (
            grouped.map(([siteId, rows]) => (
              <section className="panel keepalive-site" key={siteId}>
                <header className="panel-header">
                  <div className="panel-title">
                    <h3>{rows[0]!.site_name || `#${siteId}`}</h3>
                    <span className={policyClass(rows[0]!)}>{policyLabel(rows[0]!, t)}</span>
                  </div>
                  <div className="toolbar">
                    <Button
                      variant="quiet"
                      icon={<Settings2 size={14} />}
                      onClick={() => setEditingSite(editingSite === siteId ? null : siteId)}
                    >
                      {t("channels.keepalive.configureSite")}
                    </Button>
                  </div>
                </header>

                {editingSite === siteId ? (
                  <SitePolicyForm
                    siteId={siteId}
                    target={rows[0]!}
                    onDone={() => {
                      setEditingSite(null);
                      void status.refetch();
                    }}
                    onCancel={() => setEditingSite(null)}
                  />
                ) : null}

                <table className="keepalive-table">
                  <thead>
                    <tr>
                      <th>{t("channels.keepalive.columnChannel")}</th>
                      <th>{t("channels.keepalive.columnWindow")}</th>
                      <th>{t("channels.keepalive.columnIdle")}</th>
                      <th>{t("channels.keepalive.columnRemaining")}</th>
                      <th>{t("channels.keepalive.columnState")}</th>
                      <th />
                    </tr>
                  </thead>
                  <tbody>
                    {rows.map((row) => (
                      <tr key={row.channel_id}>
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
                            icon={<BellRing size={14} />}
                            disabled={Boolean(row.skip_reason) || sendNow.isPending}
                            onClick={() => {
                              setNotice(null);
                              sendNow.mutate(row.channel_id);
                            }}
                          >
                            {t("channels.keepalive.sendNow")}
                          </Button>
                        </td>
                      </tr>
                    ))}
                  </tbody>
                </table>
              </section>
            ))
          )}

          <section className="panel keepalive-footprint">
            <header className="panel-header">
              <div className="panel-title">
                <h3>{t("channels.keepalive.footprint")}</h3>
              </div>
            </header>
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
          </section>
        </>
      ) : null}
    </Dialog>
  );
}

/**
 * The per-site editor. The window is the site's rule, so it is edited here once
 * for the whole site rather than per channel — a channel only overrides whether
 * it participates.
 */
function SitePolicyForm({
  siteId,
  target,
  onDone,
  onCancel,
}: {
  siteId: number;
  target: KeepaliveTarget;
  onDone: () => void;
  onCancel: () => void;
}) {
  const { t } = useI18n();
  const { client } = useSession();
  const [form, setForm] = useState<SiteKeepaliveInput>({
    call_policy: target.call_policy || "allow_probe",
    keepalive_enabled: target.config.enabled,
    keepalive_idle_days: target.config.idle_days,
    keepalive_safety_margin_days: target.config.safety_margin_days,
    keepalive_model: "",
    keepalive_prompt: target.config.prompt ?? "",
    keepalive_max_tokens: target.config.max_tokens ?? 0,
    keepalive_daily_cap: target.config.daily_cap ?? 0,
    keepalive_quiet_hours: target.config.quiet_hours ?? "",
  });

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
      </div>
    </form>
  );
}

/** Grouping keeps a site's channels together, because the window belongs to the site. */
function groupBySite(targets: KeepaliveTarget[]): Array<[number, KeepaliveTarget[]]> {
  const groups = new Map<number, KeepaliveTarget[]>();
  for (const target of targets) {
    const rows = groups.get(target.site_id);
    if (rows) rows.push(target);
    else groups.set(target.site_id, [target]);
  }
  return [...groups.entries()];
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
  const rounded = Math.round(value * 10) / 10;
  return `${rounded} ${"d"}`;
}

function formatTime(value: string): string {
  const parsed = new Date(value);
  if (Number.isNaN(parsed.getTime())) return value;
  return parsed.toLocaleString();
}
