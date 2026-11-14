import { useState } from "react";
import type { Route, RouteOverview } from "../../api/types";
import { UnifyDialog } from "./UnifyDialog";
import { UnifyHistory } from "./UnifyHistory";
import { ProbeDialog } from "./ProbeDialog";
import { AggregateModelsDialog } from "./AggregateModelsDialog";

import { CapabilityRegistryDialog } from "./CapabilityRegistry";
import { AutoMatchMembersDialog } from "./AutoMatchMembersDialog";

/**
 * The models board's tools: the panels that answer questions *about* the
 * catalogue rather than edit it (capabilities, unify, unify history,
 * upstream changes, aggregates), plus the auto-match dialog that attaches
 * channels to a route.
 *
 * The open/close flags belong to the tools, so they live here — the page keeps
 * only the menu that opens them (it owns the header), and the two things
 * auto-match needs from the board's selection arrive as props.
 */
export type ModelTools = {
  probeOpen: boolean;
  setProbeOpen: (open: boolean) => void;

  capabilitiesOpen: boolean;
  setCapabilitiesOpen: (open: boolean) => void;
  unifyOpen: boolean;
  setUnifyOpen: (open: boolean) => void;
  unifyHistoryOpen: boolean;
  setUnifyHistoryOpen: (open: boolean) => void;
  aggregatesOpen: boolean;
  setAggregatesOpen: (open: boolean) => void;
  /** The change watcher is opened by an incrementing request, not a boolean. */
  changesOpenRequest: number;
  openChanges: () => void;
};

export function useModelTools(): ModelTools {
  const [probeOpen, setProbeOpen] = useState(false);

  const [capabilitiesOpen, setCapabilitiesOpen] = useState(false);
  const [unifyOpen, setUnifyOpen] = useState(false);
  const [unifyHistoryOpen, setUnifyHistoryOpen] = useState(false);
  const [aggregatesOpen, setAggregatesOpen] = useState(false);
  const [changesOpenRequest, setChangesOpenRequest] = useState(0);
  return {
    probeOpen,
    setProbeOpen,

    capabilitiesOpen,
    setCapabilitiesOpen,
    unifyOpen,
    setUnifyOpen,
    unifyHistoryOpen,
    setUnifyHistoryOpen,
    aggregatesOpen,
    setAggregatesOpen,
    changesOpenRequest,
    openChanges: () => setChangesOpenRequest((value) => value + 1),
  };
}

export function ModelToolDialogs({
  tools,
  autoMatchRoute,
  setAutoMatchRoute,
  selectedRoute,
  activeGroup,
  visibleMembers,
}: {
  tools: ModelTools;
  autoMatchRoute: Route | null;
  setAutoMatchRoute: (route: Route | null) => void;
  selectedRoute: Route | null;
  activeGroup: string;
  visibleMembers: RouteOverview["members"];
}) {
  return (
    <>
      {tools.unifyOpen ? <UnifyDialog onClose={() => tools.setUnifyOpen(false)} /> : null}
      {tools.unifyHistoryOpen ? (
        <UnifyHistory onClose={() => tools.setUnifyHistoryOpen(false)} />
      ) : null}
      {tools.probeOpen ? <ProbeDialog onClose={() => tools.setProbeOpen(false)} /> : null}
      {tools.aggregatesOpen ? (
        <AggregateModelsDialog onClose={() => tools.setAggregatesOpen(false)} />
      ) : null}

      {tools.capabilitiesOpen ? (
        <CapabilityRegistryDialog onClose={() => tools.setCapabilitiesOpen(false)} />
      ) : null}
      {autoMatchRoute && selectedRoute ? (
        <AutoMatchMembersDialog
          route={autoMatchRoute}
          group={activeGroup}
          attachedChannelIds={visibleMembers.map((candidate) => candidate.channel.id)}
          onClose={() => setAutoMatchRoute(null)}
        />
      ) : null}
    </>
  );
}
