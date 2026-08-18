import { Plus, Play, RefreshCw, Trash2 } from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { api } from "../../api/client";
import type { ExternalCheckin } from "../../api/types";
import { useAdminMutation } from "../../hooks/useAdminMutation";
import { useI18n } from "../../i18n";
import { useSession } from "../../session";
import { useToast } from "../../toast";
import {
  Button,
  ConfirmDialog,
  Dialog,
  ErrorState,
  Field,
  InfoTip,
  Loading,
  Panel,
} from "../../components/ui";

const EXTERNAL_KEYS = [["external-checkins"], ["checkin-logs"]];

/**
 * External check-in sites: generic cookie-authenticated daily check-in for
 * platforms outside the New-API family (e.g. 薄荷公益站 up.x666.me). The same
 * scheduler, logs, manual runs and alerts as regular credentials.
 */
export function ExternalCheckinsPanel() {
  const { client } = useSession();
  const { t } = useI18n();
  const toast = useToast();
  const s = api(client!);
  const qc = useQueryClient();
  const [editing, setEditing] = useState<ExternalCheckin | null>(null);
  const [creating, setCreating] = useState<{ open: boolean } | null>(null);
  const [confirmDelete, setConfirmDelete] = useState<ExternalCheckin | null>(
    null,
  );

  const list = useQuery({
    queryKey: ["external-checkins"],
    queryFn: ({ signal }) => s.externalCheckins(signal),
  });
  const invalidate = () => {
    void qc.invalidateQueries({ queryKey: ["external-checkins"] });
    void qc.invalidateQueries({ queryKey: ["checkin-logs"] });
    void qc.invalidateQueries({ queryKey: ["channel-overviews"] });
    void qc.invalidateQueries({ queryKey: ["credentials"] });
  };
  const run = useAdminMutation({
    mutationFn: (credentialId: number) => s.runCredential(credentialId),
    invalidateKeys: EXTERNAL_KEYS,
    pendingIdOf: (id) => id,
    onSuccess: () => {
      toast.push({ tone: "success", message: t("ops.external.runDone") });
    },
  });
  const toggle = useAdminMutation({
    mutationFn: (input: { id: number; enabled: boolean }) =>
      s.setCheckin(input.id, input.enabled),
    invalidateKeys: EXTERNAL_KEYS,
    pendingIdOf: (input) => input.id,
  });
  const remove = useAdminMutation({
    mutationFn: (siteId: number) => s.deleteExternalCheckin(siteId),
    invalidateKeys: EXTERNAL_KEYS,
    onSuccess: () => setConfirmDelete(null),
  });

  return (
    <Panel
      title={t("ops.external.title")}
      titleHelp={t("ops.external.titleHint")}
      actions={
        <Button
          variant="secondary"
          icon={<Plus size={14} />}
          onClick={() => setCreating({ open: true })}
        >
          {t("ops.external.add")}
        </Button>
      }
    >
      <div className="ops-panel-context">
        <span>{t("ops.external.hint")}</span>
        <InfoTip label={t("ops.external.hint")} />
      </div>
      {list.isPending ? (
        <Loading />
      ) : list.isError ? (
        <ErrorState error={list.error} />
      ) : !list.data?.length ? (
        <p className="detail-empty">{t("ops.external.empty")}</p>
      ) : (
        <div className="external-checkin-list">
          {list.data.map((item) => (
            <div className="external-checkin-row" key={item.site_id}>
              <div className="external-checkin-main">
                <strong>{item.name}</strong>
				<small className="mono truncate" title={item.base_url}>
					{item.base_url}
					{item.checkin_path
						? ` · ${item.checkin_method || "POST"} ${item.checkin_path}`
						: ""}
					{!item.has_cookie ? ` · ${t("ops.external.noCookie")}` : ""}
				</small>
              </div>
              <div className="external-checkin-actions">
                <label className="check">
                  <input
                    type="checkbox"
                    checked={item.checkin_enabled}
                    disabled={toggle.pendingId === item.credential_id}
                    onChange={(e) =>
                      toggle.mutate({
                        id: item.credential_id,
                        enabled: e.target.checked,
                      })
                    }
                  />
                  <span>{t("ops.external.scheduled")}</span>
                </label>
                <Button
                  variant="secondary"
                  icon={<Play size={14} />}
                  disabled={run.pendingId === item.credential_id}
                  onClick={() => run.mutate(item.credential_id)}
                >
                  {run.pendingId === item.credential_id
                    ? t("common.working")
                    : t("ops.external.run")}
                </Button>
                <Button
                  variant="secondary"
                  icon={<RefreshCw size={14} />}
                  onClick={() => setEditing(item)}
                >
                  {t("common.edit")}
                </Button>
                <Button
                  variant="quiet"
                  icon={<Trash2 size={14} />}
                  onClick={() => setConfirmDelete(item)}
                >
                  {t("store.uninstall")}
                </Button>
              </div>
            </div>
          ))}
        </div>
      )}
      {creating || editing ? (
        <ExternalCheckinDialog
          existing={editing}
          service={s}
          onClose={() => {
            setCreating(null);
            setEditing(null);
          }}
          onSaved={invalidate}
        />
      ) : null}
      {confirmDelete ? (
        <ConfirmDialog
          title={t("ops.external.deleteTitle")}
          message={t("ops.external.deleteConfirm", {
            name: confirmDelete.name,
          })}
          confirmLabel={t("store.uninstall")}
          pending={remove.isPending}
          error={remove.error}
          onClose={() => setConfirmDelete(null)}
          onConfirm={() => remove.mutate(confirmDelete.site_id)}
        />
      ) : null}
    </Panel>
  );
}

function ExternalCheckinDialog({
  existing,
  service,
  onClose,
  onSaved,
}: {
  existing: ExternalCheckin | null;
  service: ReturnType<typeof api>;
  onClose: () => void;
  onSaved: () => void;
}) {
  const { t } = useI18n();
  const [name, setName] = useState(existing?.name ?? "");
  const [baseUrl, setBaseUrl] = useState(existing?.base_url ?? "");
  const [checkinPath, setCheckinPath] = useState(
    existing?.checkin_path ?? "/api/checkin/spin",
  );
  const [method, setMethod] = useState(existing?.checkin_method ?? "POST");
  const [cookie, setCookie] = useState("");
  const [enabled, setEnabled] = useState(existing?.checkin_enabled ?? true);
  const [error, setError] = useState("");
  const canSubmit = Boolean(
    baseUrl.trim() && cookie.trim() && checkinPath.trim(),
  );
  const save = useAdminMutation({
    mutationFn: () => {
      const base = {
        name: name.trim() || undefined,
        base_url: baseUrl.trim(),
        checkin_path: checkinPath.trim(),
        checkin_method: method,
        enabled,
      };
      if (existing) {
        return service.updateExternalCheckin(existing.site_id, {
          ...base,
          // Empty cookie on edit = keep the stored value (mask semantics).
          ...(cookie.trim() ? { cookie: cookie.trim() } : {}),
        });
      }
      return service.createExternalCheckin({
        ...base,
        cookie: cookie.trim(),
      });
    },
    invalidateKeys: EXTERNAL_KEYS,
    toastOnError: false,
    onSuccess: () => {
      onSaved();
      onClose();
    },
    onError: (err: unknown) => {
      setError(err instanceof Error ? err.message : String(err));
    },
  });

  return (
    <Dialog
      title={
        existing
          ? t("ops.external.editTitle")
          : t("ops.external.addTitle")
      }
      onClose={onClose}
      actions={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            disabled={save.isPending || (existing == null && !canSubmit)}
            onClick={() => save.mutate(undefined)}
          >
            {save.isPending ? t("common.working") : t("common.save")}
          </Button>
        </>
      }
    >
      <div className="form-stack">
        <Field label={t("ops.external.name")}>
          <input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="薄荷公益站"
            disabled={save.isPending}
          />
        </Field>
        <Field label={t("ops.external.baseUrl")} hint={t("ops.external.baseUrlHint")}>
          <input
            type="url"
            value={baseUrl}
            onChange={(e) => setBaseUrl(e.target.value)}
            placeholder="https://up.x666.me"
            disabled={save.isPending}
          />
        </Field>
        <div className="form-grid">
          <Field label={t("ops.external.path")}>
            <input
              className="mono"
              value={checkinPath}
              onChange={(e) => setCheckinPath(e.target.value)}
              placeholder="/api/checkin/spin"
              disabled={save.isPending}
            />
          </Field>
          <Field label={t("ops.external.method")}>
            <select
              value={method}
              onChange={(e) => setMethod(e.target.value)}
              disabled={save.isPending}
            >
              <option value="POST">POST</option>
              <option value="GET">GET</option>
            </select>
          </Field>
        </div>
        <Field
          label={t("ops.external.cookie")}
          hint={
            existing?.has_cookie
              ? t("ops.external.cookieKeepHint")
              : t("ops.external.cookieHint")
          }
        >
          <input
            type="password"
            autoComplete="new-password"
            value={cookie}
            onChange={(e) => setCookie(e.target.value)}
            placeholder={
              existing?.has_cookie
                ? t("common.maskedPlaceholder")
                : "auth_token=…"
            }
            disabled={save.isPending}
          />
        </Field>
        <label className="check" style={{ marginTop: 4 }}>
          <input
            type="checkbox"
            checked={enabled}
            onChange={(e) => setEnabled(e.target.checked)}
            disabled={save.isPending}
          />
          <span>{t("ops.external.enableSchedule")}</span>
        </label>
      </div>
      {error ? <ErrorState error={error} /> : null}
    </Dialog>
  );
}