import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import type { api } from "../../api/client";
import type { Channel, Site } from "../../api/types";
import { positiveId } from "../../lib/positiveId";
import { writeChannelTab } from "./tabState";

/** Whatever the client's own method returns — the queries must not restate it. */
type Service = ReturnType<typeof api>;
type Resolved<T> = T extends Promise<infer U> ? U : never;

/**
 * The connections board's own state: what it is showing, which channel it has
 * selected, which overlays are open, and the three queries the list is built from.
 *
 * This is the piece the A02 split called the state owner. Before it, the page
 * component held the queries, the six dialog switches, three one-shot deep-link
 * refs, the inspector flag, the context menu position, the "what just happened"
 * banner and the credentials pool — all interleaved with eighteen mutations and
 * the page's markup, so "what owns this channel's data?" had no answer short of
 * reading two thousand lines.
 *
 * Three rules live here and are worth naming, because they are the ones that were
 * easy to get wrong when they were implicit:
 *
 *  - **the URL is the truth for selection** (`?id=`): the value is read from the
 *    query string, written back on click, and the deep links (?channel=, ?keys=)
 *    resolve to it as well;
 *  - **deep links fire once per navigation**: `setX(null)` commits before the
 *    router's param transition, so without the one-shot markers the effect re-runs
 *    against a stale URL and re-opens the drawer the operator just closed;
 *  - **the credentials pool follows the overlay that shows it**, not the list
 *    selection — otherwise a `?keys=` deep link would list one channel's keys under
 *    another channel's title.
 */
type Text = (key: string, vars?: Record<string, string | number>) => string;

export type ChannelBoard = {
  overviews: UseQueryResult<Resolved<ReturnType<Service["channelOverviews"]>>>;
  sites: UseQueryResult<Resolved<ReturnType<Service["sites"]>>>;
  routeOverviewsQuery: UseQueryResult<Resolved<ReturnType<Service["routeOverviews"]>>>;
  credentials: UseQueryResult<Resolved<ReturnType<Service["credentials"]>>>;
  siteById: Map<number, Site>;
  /* Dialogs. */
  addOpen: boolean;
  setAddOpen: (open: boolean) => void;
  remove: Channel | null;
  setRemove: (channel: Channel | null) => void;
  edit: Channel | null;
  setEdit: (channel: Channel | null) => void;
  modelsChannel: Channel | null;
  setModelsChannel: (channel: Channel | null) => void;
  keysChannel: Channel | null;
  setKeysChannel: (channel: Channel | null) => void;
  createKeyChannel: Channel | null;
  setCreateKeyChannel: (channel: Channel | null) => void;
  createKeyLocked: { current: boolean };
  closeModelsDrawer: () => void;
  /* Selection and overlays. */
  selectedId: number | undefined;
  inspectorOpen: boolean;
  setInspectorOpen: (open: boolean) => void;
  contextMenu: { channelId: number; top: number; left: number } | null;
  setContextMenu: (value: { channelId: number; top: number; left: number } | null) => void;
  /* The banner describing what the last action did. */
  stageMessage: {
    kind: "created" | "created_and_verified" | "verify_failed";
    name: string;
    channelId: number;
    models?: number;
  } | null;
  setStageMessage: (value: ChannelBoard["stageMessage"]) => void;
};

export function useChannelBoard({
  service,
  params,
  setParams,
}: {
  service: ReturnType<typeof api>;
  params: URLSearchParams;
  setParams: (next: URLSearchParams, options?: { replace: boolean }) => void;
  /** Reserved for messages the board may need to word itself. */
  t?: Text;
}): ChannelBoard {
  // Cooldown counters (`cooling_member_count`, `failure_count`) are computed live
  // by the backend from `route_members.cooldown_until`, and the degraded verdict
  // plus its reason tooltip hang off them. Without an interval of our own the page
  // only refreshed on the shell's 30s tick, so a channel entering or leaving
  // cooldown looked stuck until the operator switched pages.
  const overviews = useQuery({
    queryKey: ["channel-overviews"],
    queryFn: ({ signal }) => service.channelOverviews(signal),
    refetchInterval: 15_000,
  });
  const sites = useQuery({
    queryKey: ["sites"],
    queryFn: ({ signal }) => service.sites(signal),
  });
  const routeOverviewsQuery = useQuery({
    queryKey: ["route-overviews"],
    queryFn: ({ signal }) => service.routeOverviews(signal),
  });

  const [addOpen, setAddOpen] = useState(false);
  const [remove, setRemove] = useState<Channel | null>(null);
  const [edit, setEdit] = useState<Channel | null>(null);
  const [modelsChannel, setModelsChannel] = useState<Channel | null>(null);
  const [keysChannel, setKeysChannel] = useState<Channel | null>(null);
  const [createKeyChannel, setCreateKeyChannel] = useState<Channel | null>(null);
  // Synchronous lock for the create-key dialog (see its onCreate re-entry guard).
  const createKeyLocked = useRef(false);
  // Channel id the deep-link effect already popped the models drawer for.
  const deepLinkOpened = useRef<number | null>(null);
  // Same one-shot guard for the ?keys= deep-link (log chain → this channel's keys).
  const keysDeepLinkOpened = useRef<number | null>(null);
  const [contextMenu, setContextMenu] = useState<{
    channelId: number;
    top: number;
    left: number;
  } | null>(null);
  const [stageMessage, setStageMessage] = useState<ChannelBoard["stageMessage"]>(null);
  const selectedId = positiveId(params.get("id"));
  const [inspectorOpen, setInspectorOpen] = useState(Boolean(selectedId));

  // Deep-link from the models page (?channel=<id>): pop that channel's model
  // management drawer open so the user lands directly on the right tab.
  useEffect(() => {
    const target = positiveId(params.get("channel"));
    if (!target || modelsChannel?.id === target) return;
    // One-shot per navigation: re-running with the same target (a close committing
    // before the router's param transition) must not re-open.
    if (deepLinkOpened.current === target) return;
    const overview = (overviews.data ?? []).find((entry) => entry.channel.id === target)?.channel;
    if (overview) {
      deepLinkOpened.current = target;
      setModelsChannel(overview);
      const next = new URLSearchParams(params);
      next.delete("channel");
      setParams(next, { replace: true });
    }
  }, [params, overviews.data, modelsChannel, setParams]);

  // Closing strips the deep-link ?channel= param too: the auto-select effects run
  // in the same commit that opens the drawer and re-add the param from the stale
  // searchParams snapshot, and a surviving param would make the deep-link effect
  // re-open the drawer right after this close.
  const closeModelsDrawer = () => {
    setModelsChannel(null);
    if (params.has("channel")) {
      const next = new URLSearchParams(params);
      next.delete("channel");
      setParams(next, { replace: true });
    }
  };

  // Deep-link from the log page's request chain (?keys=<id>): the chain names the
  // upstream key that served an attempt, and this is the list that owns it.
  useEffect(() => {
    const target = positiveId(params.get("keys"));
    if (!target || keysChannel?.id === target) return;
    if (keysDeepLinkOpened.current === target) return;
    const overview = (overviews.data ?? []).find((entry) => entry.channel.id === target)?.channel;
    if (overview) {
      keysDeepLinkOpened.current = target;
      setKeysChannel(overview);
      // Select the row the drawer belongs to as well. The linked channel arrives
      // without ?id=, so the list ran its own auto-select and the page ended up
      // pointing at two channels at once — drawer titled one, highlighted row
      // another.
      writeChannelTab("selected", target);
      const next = new URLSearchParams(params);
      next.delete("keys");
      next.set("id", String(target));
      setParams(next, { replace: true });
    }
  }, [params, overviews.data, keysChannel, setParams]);

  // Load site credentials for the surface that is about to render them. The keys
  // drawer owns the pool it shows, so its channel decides the fetch: otherwise the
  // pool follows the list selection, and a deep-link (?keys=<id>) listed one
  // channel's keys under another channel's title.
  const credentialSiteId = keysChannel
    ? // A site-less channel has no pool at all — never borrow the selection's.
      (keysChannel.site_id ?? undefined)
    : (edit?.site_id ??
      // The channel selected, or opened via ⋯/context menu.
      (selectedId != null
        ? (overviews.data ?? []).find((row) => row.channel.id === selectedId)?.channel.site_id
        : undefined) ??
      (contextMenu != null
        ? (overviews.data ?? []).find((row) => row.channel.id === contextMenu.channelId)?.channel
            .site_id
        : undefined));
  const credentials = useQuery({
    queryKey: ["credentials", credentialSiteId],
    queryFn: ({ signal }) => service.credentials(credentialSiteId as number, signal),
    enabled: typeof credentialSiteId === "number" && credentialSiteId > 0,
  });

  const siteById = useMemo(
    () => new Map((sites.data ?? []).map((site) => [site.id, site])),
    [sites.data],
  );

  return {
    overviews,
    sites,
    routeOverviewsQuery,
    credentials,
    siteById,
    addOpen,
    setAddOpen,
    remove,
    setRemove,
    edit,
    setEdit,
    modelsChannel,
    setModelsChannel,
    keysChannel,
    setKeysChannel,
    createKeyChannel,
    setCreateKeyChannel,
    createKeyLocked,
    closeModelsDrawer,
    selectedId,
    inspectorOpen,
    setInspectorOpen,
    contextMenu,
    setContextMenu,
    stageMessage,
    setStageMessage,
  };
}
