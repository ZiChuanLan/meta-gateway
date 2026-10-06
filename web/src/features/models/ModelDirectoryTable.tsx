import type { KeyboardEvent, MouseEvent, ReactNode } from "react";
import { StatusBadge } from "../../components/ui";
import { useI18n } from "../../i18n";
import { formatTokens } from "../../lib/format";

/**
 * The model directory table, shared by the console's Models page and the
 * member app's model catalogue.
 *
 * The two sides list the same object — "a model a client can call" — but from
 * different sources: the console reads route overviews (pattern → members),
 * the member app reads its authorized catalogue (`/me/model-catalog`). This
 * component owns the SHARED presentation: the name cell (identifier, group +
 * metadata badges), the optional upstream and status columns, and the
 * trailing actions column. Everything caller-specific arrives as plain row
 * slots, so neither side re-renders a second table and the two lists cannot
 * drift apart.
 *
 * A column that does not apply to a viewer is removed (`showUpstream` /
 * `showStatus` false), never rendered empty — the same rule the keys/logs
 * sources use for operator-only columns.
 *
 * The markup mirrors the console's original directory table exactly (the
 * `model-upstream-header` / `status-col` / `actions` header classes, the
 * `data-columns` count, the bulk checkbox column) because the workspaces
 * sheets key responsive behaviour off those names.
 */
export type DirectoryRow = {
  /** The callable model name (route pattern or catalogue name). */
  name: string;
  /** Auto/manual group shown as a badge in the name cell. */
  group?: string;
  /** Extra metadata badges: context window, thinking, vendor. */
  contextWindow?: number;
  supportsThinking?: boolean;
  vendor?: string;
  /** Free-form badges beside the name (shim hint, "已自定义", …). */
  badges?: ReactNode;
  prices?: ReactNode;
  facts?: ReactNode;
  /** Small provider line under the name (console: primary channel). */
  provider?: ReactNode;
  /** Upstream cell: console renders channel links, member app vendor·kind. */
  upstream?: ReactNode;
  /** Row status. The console derives ready/unverified/disabled from members;
   *  a catalogue row is always enabled. */
  status: "ready" | "disabled" | "unverified" | "enabled" | "unavailable";
  /** Row actions (console ActionMenu, member buttons). Rendered in the last
   *  cell; clicks there never select the row. */
  actions?: ReactNode;
  /** Bulk-selection checkbox cell (console bulk mode only). */
  bulkCell?: ReactNode;
} & {
  onClick?: (event: MouseEvent<HTMLTableRowElement>) => void;
  onKeyDown?: (event: KeyboardEvent<HTMLTableRowElement>) => void;
  onContextMenu?: (event: MouseEvent<HTMLTableRowElement>) => void;
  tabIndex?: number;
  className?: string;
  /** Optional explicit React key; defaults to the row name. */
  key?: string;
};

function formatContext(value: number): string {
  // Same compact notation as everywhere else tokens are shown (1.2M / 34.5k),
  // so a badge and a table cell never disagree about the same number.
  return formatTokens(value);
}

export function ModelDirectoryTable({
  rows,
  showUpstream,
  showStatus = true,
  bulkMode = false,
  bulkHeader,
  onBulkCellClick,
  className = "model-directory-table",
}: {
  rows: DirectoryRow[];
  /** Upstream column: the console only (a member never sees upstreams). */
  showUpstream: boolean;
  /** Status column: off for the member catalogue (every row is usable). */
  showStatus?: boolean;
  /** Leading checkbox column (console bulk selection). */
  bulkMode?: boolean;
  bulkHeader?: ReactNode;
  /** Clicks inside a bulk cell must not select the (clickable) row. */
  onBulkCellClick?: (event: MouseEvent<HTMLTableCellElement>) => void;
  className?: string;
}) {
  const { t } = useI18n();
  const columns =
    (bulkMode ? 1 : 0) + 1 + (showUpstream ? 1 : 0) + (showStatus ? 1 : 0) + 1;
  return (
    <div className="table-wrap" data-columns={columns}>
      <table className={className}>
        <thead>
          <tr>
            {bulkMode ? <th className="bulk-cell">{bulkHeader}</th> : null}
            <th>{t("common.model")}</th>
            {showUpstream ? (
              <th className="model-upstream-header">{t("modelsPage.col.upstream")}</th>
            ) : null}
            {showStatus ? <th className="status-col">{t("common.status")}</th> : null}
            <th className="actions">{t("common.actions")}</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((row) => (
            <tr
              key={row.key ?? row.name}
              data-model-selectable={row.onClick ? "true" : undefined}
              tabIndex={row.tabIndex}
              className={row.className}
              onClick={row.onClick}
              onKeyDown={row.onKeyDown}
              onContextMenu={row.onContextMenu}
            >
              {bulkMode ? (
                <td className="bulk-cell" onClick={onBulkCellClick}>
                  {row.bulkCell}
                </td>
              ) : null}
              <td className="model-row-name">
                <strong className="mono" title={row.name}>
                  {row.name}
                </strong>
                {row.provider}
                {row.group ? (
                  <span className="model-meta-badge is-group">{row.group}</span>
                ) : null}
                {row.badges}
                <span className="model-meta-badges">
                  {row.contextWindow && row.contextWindow > 0 ? (
                    <span className="model-meta-badge" title={t("modelsPage.metaCtx")}>
                      {formatContext(row.contextWindow)}
                    </span>
                  ) : null}
                  {row.supportsThinking ? (
                    <span
                      className="model-meta-badge is-thinking"
                      title={t("modelsPage.metaThinking")}
                    >
                      {t("modelsPage.metaThinkingShort")}
                    </span>
                  ) : null}
                  {row.vendor ? (
                    <span className="model-meta-badge" title={t("modelsPage.metaVendor")}>
                      {row.vendor}
                    </span>
                  ) : null}
                </span>
                {row.facts}
                {row.prices}
              </td>
              {showUpstream ? (
                <td className="model-row-upstream">{row.upstream}</td>
              ) : null}
              {showStatus ? (
                <td className="status-col model-row-status">
                  <StatusBadge value={row.status} />
                </td>
              ) : null}
              <td
                className="actions row-actions"
                onClick={(event) => event.stopPropagation()}
              >
                {row.actions}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
