import { useEffect, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { ChevronDown, ChevronUp, GripVertical, RotateCcw } from "lucide-react";
import { Button, Dialog, Loading } from "../components/ui";
import { accountRequest } from "../team/transport";
import { teamError, teamText } from "../team/text";
import type { RouteOrder, RouteUpstream } from "../team/types";

/**
 * Arrange one model's upstreams.
 *
 * The list IS the order: dragging a row to the top makes it the first channel
 * failover walks, and switching a row off takes it out of this model for this
 * user only. Both facts are shown on the row itself (rank, site default) so a
 * member can reason about routing without knowing what a priority number is.
 */
export function RouteOrderDialog({
  model,
  planId,
  locale,
  onClose,
  onSaved,
}: {
  model: string;
  planId: number;
  locale: string;
  onClose: () => void;
  onSaved: () => void;
}) {
  const t = teamText(locale);
  const qc = useQueryClient();
  const [rows, setRows] = useState<RouteUpstream[] | null>(null);
  const [dragIndex, setDragIndex] = useState<number | null>(null);
  const [overIndex, setOverIndex] = useState<number | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<unknown>(null);
  const [notice, setNotice] = useState("");

  const query = useQuery({
    queryKey: ["user", "route-order", model, planId],
    queryFn: ({ signal }) =>
      accountRequest<RouteOrder>(
        `/me/routes/${encodeURIComponent(model)}${planId ? `?plan_id=${planId}` : ""}`,
        { signal },
      ),
  });
  useEffect(() => {
    if (query.data) setRows(query.data.upstreams.map((row) => ({ ...row })));
  }, [query.data]);

  function move(from: number, to: number) {
    setRows((current) => {
      if (!current || from === to || to < 0 || to >= current.length) return current;
      const item = current[from];
      if (!item) return current;
      const next = [...current];
      next.splice(from, 1);
      next.splice(to, 0, item);
      return next;
    });
    setNotice("");
  }

  function patch(id: number, values: Partial<RouteUpstream>) {
    setRows(
      (current) => current?.map((row) => (row.id === id ? { ...row, ...values } : row)) ?? current,
    );
    setNotice("");
  }

  async function save() {
    if (!rows) return;
    setBusy(true);
    setError(null);
    try {
      await accountRequest(`/me/routes/${encodeURIComponent(model)}`, {
        method: "PUT",
        body: JSON.stringify({
          plan_id: planId,
          entries: rows.map((row) => ({
            id: row.id,
            weight: row.weight,
            disabled: row.disabled,
          })),
        }),
      });
      setNotice(t("arrangeSaved"));
      await query.refetch();
      onSaved();
      await qc.invalidateQueries({ queryKey: ["user", "routes"] });
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }

  async function restore() {
    setBusy(true);
    setError(null);
    try {
      await accountRequest(
        `/me/routes/${encodeURIComponent(model)}${planId ? `?plan_id=${planId}` : ""}`,
        { method: "DELETE" },
      );
      setNotice(t("restored"));
      await query.refetch();
      onSaved();
      await qc.invalidateQueries({ queryKey: ["user", "routes"] });
    } catch (e) {
      setError(e);
    } finally {
      setBusy(false);
    }
  }

  const dirty = Boolean(
    rows &&
    query.data &&
    JSON.stringify(rows.map((r) => [r.id, r.weight, r.disabled])) !==
      JSON.stringify(query.data.upstreams.map((r) => [r.id, r.weight, r.disabled])),
  );

  return (
    <Dialog
      title={`${model} · ${t("arrange")}`}
      onClose={onClose}
      busy={busy}
      actions={
        <>
          {query.data?.customized ? (
            <Button
              variant="quiet"
              icon={<RotateCcw size={14} />}
              disabled={busy}
              onClick={() => void restore()}
            >
              {t("restoreSiteOrder")}
            </Button>
          ) : null}
          <Button variant="secondary" onClick={onClose} disabled={busy}>
            {t("close")}
          </Button>
          <Button loading={busy} disabled={busy || !dirty} onClick={() => void save()}>
            {busy ? t("saving") : t("save")}
          </Button>
        </>
      }
    >
      <p className="workspace-note" style={{ marginTop: 0 }}>
        {t("arrangeHint")}
      </p>
      {query.isPending ? (
        <Loading />
      ) : query.error ? (
        <div className="team-error" role="alert">
          {teamError(query.error, locale)}
        </div>
      ) : !rows?.length ? (
        <p className="workspace-note">{t("arrangeEmpty")}</p>
      ) : (
        <ol className="route-order-list">
          {rows.map((row, index) => (
            <li
              key={row.id}
              className={`route-order-row${row.disabled ? " is-off" : ""}${
                overIndex === index && dragIndex !== null && dragIndex !== index
                  ? " is-drop-target"
                  : ""
              }`}
              draggable={!busy}
              onDragStart={() => setDragIndex(index)}
              onDragOver={(event) => {
                event.preventDefault();
                setOverIndex(index);
              }}
              onDrop={(event) => {
                event.preventDefault();
                if (dragIndex !== null) move(dragIndex, index);
                setDragIndex(null);
                setOverIndex(null);
              }}
              onDragEnd={() => {
                setDragIndex(null);
                setOverIndex(null);
              }}
            >
              <span className="route-order-handle" title={t("dragHandle")}>
                <GripVertical size={15} />
              </span>
              <span className="route-order-rank">{index + 1}</span>
              <span className="route-order-name">
                <strong>{row.channel}</strong>
                <small>
                  {row.origin ? `${t("originUpstream")}: ${row.origin}` : t("sameAsRouteName")} · #
                  {row.id}
                  {row.group && row.group !== "default" ? ` · ${row.group}` : ""}
                </small>
              </span>
              <label className="route-order-weight">
                <span className="workspace-caption">{t("upstreamWeight")}</span>
                <input
                  type="number"
                  min={1}
                  max={1000}
                  disabled={busy || row.disabled}
                  value={row.weight}
                  aria-label={`${t("upstreamWeight")} · ${row.channel}`}
                  onChange={(event) => patch(row.id, { weight: Number(event.target.value) || 1 })}
                />
                <small>
                  {t("siteDefaults")} {row.site_weight}
                </small>
              </label>
              <label className="route-order-toggle">
                <input
                  type="checkbox"
                  disabled={busy}
                  checked={!row.disabled}
                  onChange={(event) => patch(row.id, { disabled: !event.target.checked })}
                />
                <span>{t("participate")}</span>
              </label>
              <span className="route-order-moves">
                <button
                  type="button"
                  className="icon-button"
                  disabled={busy || index === 0}
                  aria-label={`${t("moveUp")} · ${row.channel}`}
                  onClick={() => move(index, index - 1)}
                >
                  <ChevronUp size={14} />
                </button>
                <button
                  type="button"
                  className="icon-button"
                  disabled={busy || index === rows.length - 1}
                  aria-label={`${t("moveDown")} · ${row.channel}`}
                  onClick={() => move(index, index + 1)}
                >
                  <ChevronDown size={14} />
                </button>
              </span>
            </li>
          ))}
        </ol>
      )}
      {error ? (
        <div className="team-error" role="alert">
          {teamError(error, locale)}
        </div>
      ) : null}
      {notice ? (
        <p role="status" className="workspace-note">
          {notice}
        </p>
      ) : null}
    </Dialog>
  );
}
