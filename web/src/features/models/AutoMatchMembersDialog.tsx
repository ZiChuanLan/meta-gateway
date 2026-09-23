import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { CheckCheck, ListChecks, Plus } from "lucide-react";
import type { Route } from "../../api/types";
import { api } from "../../api/client";
import { Button, Dialog, Empty, ErrorState } from "../../components/ui";
import { useAdminMutation } from "../../hooks/useAdminMutation";
import { useI18n } from "../../i18n";
import { useSession } from "../../session";
import { useToast } from "../../toast";

/** Same fan-out the Models page uses after a routing write. */
const ROUTING_INVALIDATE_KEYS = [
  ["routes"],
  ["route-overviews"],
  ["members"],
  ["channel-overviews"],
  ["models"],
  ["explain"],
] as const;

/**
 * "Attach every channel that serves this model." It opens on a preview rather
 * than acting immediately, for the same reason the add-route dialog previews
 * its auto-match: the operator sees exactly which channels a write would add —
 * and, just as importantly, which ones are already there.
 *
 * The preview is the server's own match set (the same intersection route
 * creation and the attach endpoint use), so the dialog can never promise more
 * than the write will deliver. Rows already carrying a membership in the target
 * group are listed but not offered: re-attaching them is a no-op server-side,
 * so a checkbox would misrepresent what confirming does.
 */
export function AutoMatchMembersDialog({
  route,
  group,
  attachedChannelIds,
  onClose,
}: {
  route: Route;
  /** Target member group; empty means the built-in default group. */
  group: string;
  /** Channels that already have a member in `group` for this route. */
  attachedChannelIds: number[];
  onClose: () => void;
}) {
  const { t } = useI18n();
  const { client } = useSession();
  const service = api(client!);
  const toast = useToast();

  const matches = useQuery({
    queryKey: ["model-channels", route.model_pattern],
    queryFn: ({ signal }) => service.modelChannels(route.model_pattern, signal),
  });
  const items = matches.data?.items ?? [];
  const alreadyAttached = new Set(attachedChannelIds);
  const candidates = items.filter((item) => !alreadyAttached.has(item.channel_id));
  const attachedItems = items.filter((item) => alreadyAttached.has(item.channel_id));

  // `null` means "everything the preview offered" — the default, kept as a
  // sentinel so a channel that appears between the fetch and the click is
  // still picked up rather than silently dropped from the selection.
  const [kept, setKept] = useState<Set<number> | null>(null);
  const selected = kept ?? new Set(candidates.map((item) => item.channel_id));
  const allSelected = candidates.length > 0 && selected.size === candidates.length;
  const toggle = (id: number) => {
    const next = new Set(selected);
    if (next.has(id)) next.delete(id);
    else next.add(id);
    setKept(next);
  };

  const attach = useAdminMutation({
    mutationFn: (ids: number[]) =>
      service.autoMatchRouteMembers(route.id, ids, group),
    invalidateKeys: [...ROUTING_INVALIDATE_KEYS],
    toastOnError: false,
    onSuccess: ({ added, skipped }) => {
      toast.push({
        tone: added > 0 ? "success" : "info",
        message: t(
          skipped > 0
            ? "modelsPage.autoMatch.doneSkipped"
            : "modelsPage.autoMatch.done",
          { group: group || t("routing.groupDefault"), added, skipped },
        ),
      });
      onClose();
    },
  });

  return (
    <Dialog
      title={t("modelsPage.autoMatch.title")}
      onClose={onClose}
      busy={attach.isPending}
      actions={
        <>
          <Button variant="secondary" onClick={onClose}>
            {t("common.cancel")}
          </Button>
          <Button
            icon={<Plus size={14} />}
            loading={attach.isPending}
            // An empty list is not "attach none" on the wire — the server reads
            // it as "every current match" — so nothing is sent until a channel
            // is actually ticked.
            disabled={attach.isPending || selected.size === 0}
            onClick={() => attach.mutate([...selected])}
          >
            {t("modelsPage.autoMatch.confirm", { n: selected.size })}
          </Button>
        </>
      }
    >
      <p className="panel-hint">{t("modelsPage.autoMatch.desc")}</p>
      <p className="ops-panel-context">
        {t("modelsPage.autoMatch.group", {
          name: group || t("routing.groupDefault"),
        })}
      </p>
      {matches.isPending ? (
        <p className="muted" role="status">
          {t("common.loading")}
        </p>
      ) : null}
      {matches.isError ? <ErrorState error={matches.error} /> : null}
      {matches.isSuccess && items.length === 0 ? (
        <Empty>{t("modelsPage.autoMatch.none")}</Empty>
      ) : null}
      {candidates.length > 0 ? (
        <>
          <div className="auto-match-head">
            <span className="ops-panel-context" role="status">
              {t("modelsPage.autoMatch.selected", {
                selected: selected.size,
                total: candidates.length,
              })}
            </span>
            <Button
              variant="quiet"
              icon={<CheckCheck size={13} />}
              onClick={() =>
                setKept(
                  allSelected
                    ? new Set()
                    : new Set(candidates.map((item) => item.channel_id)),
                )
              }
            >
              {t(
                allSelected
                  ? "modelsPage.autoMatch.selectNone"
                  : "modelsPage.autoMatch.selectAll",
              )}
            </Button>
          </div>
          <div className="selection-list scroll-list">
            {candidates.map((item) => (
              <label className="check" key={item.channel_id}>
                <input
                  type="checkbox"
                  // The row also carries a source chip; naming the control by
                  // the channel alone keeps the checkbox's label unambiguous.
                  aria-label={item.channel_name}
                  checked={selected.has(item.channel_id)}
                  onChange={() => toggle(item.channel_id)}
                />
                <span>{item.channel_name}</span>
                <span className="pg-chip">{item.source}</span>
              </label>
            ))}
          </div>
        </>
      ) : null}
      {matches.isSuccess && items.length > 0 && candidates.length === 0 ? (
        <Empty>{t("modelsPage.autoMatch.allAttached")}</Empty>
      ) : null}
      {attachedItems.length > 0 ? (
        <div className="auto-match-attached">
          <span className="ops-panel-context">
            <ListChecks size={12} />
            {t("modelsPage.autoMatch.attachedLabel", { n: attachedItems.length })}
          </span>
          <ul>
            {attachedItems.map((item) => (
              <li key={item.channel_id} className="mono">
                {item.channel_name}
              </li>
            ))}
          </ul>
        </div>
      ) : null}
      {attach.isError ? <ErrorState error={attach.error} /> : null}
      <p className="ops-panel-context">{t("modelsPage.autoMatch.footnote")}</p>
    </Dialog>
  );
}
