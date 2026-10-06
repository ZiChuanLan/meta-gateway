import { GripVertical, Power, RotateCcw, Shield, Target } from "lucide-react";
import type { Route, RoutingCandidate } from "../../api/types";
import { ActionMenu } from "../../components/ActionMenu";
import { StatusBadge } from "../../components/ui";
import { CooldownHint } from "./CooldownHint";
import { memberActions } from "./memberActions";
import {
  candidateState,
  isActiveCooldown,
  memberFinance,
  originModelOf,
  sortMembers,
} from "./routingPolicy";

/**
 * The routing list for one route: a row per member, with the controls that decide
 * what the gateway will actually use.
 *
 * Each row carries a lot of derived truth — where the member's model name came
 * from, the effective weight after the router's scoring, whether the operator
 * protected it from automatic changes, whether it is in cooldown or out of
 * credit, and whether a cooldown is current or already history — plus drag
 * reordering and per-row maintenance actions. It lived inside the detail panel of
 * a 2,300-line component; the calculations stay where they were (routingPolicy),
 * this file only decides how they are shown and which button does what.
 */
type Text = (key: string, vars?: Record<string, string | number>) => string;

/** The router's own explanation for a route, when the backend answered. */
type Explain = {
  data?: {
    candidates: Array<{ candidate: RoutingCandidate; score?: number; reasons: string[] }>;
  };
};

export type RouteMemberListProps = {
  t: Text;
  route: Route;
  /** The route's own model name, used when a member does not rename it. */
  selectedModel: string;
  /** Members that belong to the group currently being viewed. */
  members: RoutingCandidate[];
  explain: Explain;
  /** Per-member credit/call finance, when the board has it. */
  financeItems: Parameters<typeof memberFinance>[2];
  /* Drag reordering. */
  dragMemberId: number | null;
  setDragMemberId: (id: number | null) => void;
  reorderMembers: {
    isPending: boolean;
    mutate: (next: RoutingCandidate[]) => unknown;
  };
  /* Bulk selection over members. */
  bulkSelect: boolean;
  selectedMemberIds: Set<number>;
  toggleMemberSelect: (id: number) => void;
  /* Actions. */
  navigate: (to: string) => void;
  enableChannel: { pendingId?: number | string | null; mutate: (id: number) => unknown };
  mutations: {
    toggleMember: {
      pendingId?: number | string | null;
      mutate: (entry: RoutingCandidate["member"]) => unknown;
    };
    pinMember: {
      isPending?: boolean;
      mutate: (input: { route: Route; memberId: number | null }) => unknown;
    };
    clearHealth: {
      pendingId?: number | string | null;
      isPending?: boolean;
      mutate: (id: number) => unknown;
    };
    saveMember: { reset: () => void };
  };
  setMember: (entry: RoutingCandidate["member"]) => void;
  setRemoveMember: (entry: RoutingCandidate["member"]) => void;
};

export function RouteMemberList(props: RouteMemberListProps) {
  const {
    t,
    route,
    selectedModel,
    members,
    explain,
    financeItems,
    dragMemberId,
    setDragMemberId,
    reorderMembers,
    bulkSelect,
    selectedMemberIds,
    toggleMemberSelect,
    navigate,
    enableChannel,
    mutations,
    setMember,
    setRemoveMember,
  } = props;

  const applyOrder = (next: RoutingCandidate[]) => reorderMembers.mutate(next);

  return members.map((candidate, rowIndex) => {
    const entry = candidate.member;
    const origin = originModelOf(entry, route);
    const financeInfo = memberFinance(entry, origin || selectedModel, financeItems);
    const evaluation = explain.data?.candidates.find(
      (item) => item.candidate.member.id === entry.id,
    );
    const activeCooldown = isActiveCooldown(entry);
    const autoDisabled = candidate.channel.status === "auto_disabled";
    const state = autoDisabled
      ? "auto_disabled"
      : evaluation?.reasons.includes("circuit_open")
        ? "circuit_open"
        : candidateState(candidate);
    // An expired cooldown is history, not an actionable cooldown. A disabled member
    // still needs an explicit recovery action, unless the whole channel is parked
    // (the channel-level recovery button handles that).
    const canResetMemberHealth =
      !autoDisabled && (activeCooldown || (!entry.enabled && entry.fail_count > 0));
    const resetActionIsCooldown = activeCooldown && entry.enabled;
    const busy =
      mutations.toggleMember.pendingId === entry.id ||
      mutations.clearHealth.pendingId === entry.id ||
      reorderMembers.isPending;
    const moveBy = (delta: number) => {
      const from = members.findIndex((item) => item.member.id === entry.id);
      const to = from + delta;
      if (from < 0 || to < 0 || to >= members.length) return;
      const next = [...members];
      const temp = next[from]!;
      next[from] = next[to]!;
      next[to] = temp;
      applyOrder(next);
    };
    return (
      <div
        className={`member-row${dragMemberId === entry.id ? " is-dragging" : ""}${autoDisabled ? " is-auto-disabled" : ""}${bulkSelect && selectedMemberIds.has(entry.id) ? " is-selected" : ""}`}
        key={entry.id}
        draggable={!reorderMembers.isPending && !bulkSelect}
        onDragStart={(event) => {
          setDragMemberId(entry.id);
          event.dataTransfer.effectAllowed = "move";
          event.dataTransfer.setData("text/plain", String(entry.id));
        }}
        onDragOver={(event) => {
          event.preventDefault();
          event.dataTransfer.dropEffect = "move";
        }}
        onDrop={(event) => {
          event.preventDefault();
          const sourceId = Number(event.dataTransfer.getData("text/plain"));
          setDragMemberId(null);
          if (!sourceId || sourceId === entry.id) return;
          const current = sortMembers(members);
          const from = current.findIndex((item) => item.member.id === sourceId);
          const to = current.findIndex((item) => item.member.id === entry.id);
          if (from < 0 || to < 0) return;
          const next = [...current];
          const [moved] = next.splice(from, 1);
          if (!moved) return;
          next.splice(to, 0, moved);
          applyOrder(next);
        }}
        onDragEnd={() => setDragMemberId(null)}
      >
        {bulkSelect ? (
          <label className="member-bulk-check" title={t("routing.bulkToggleSelect")}>
            <input
              type="checkbox"
              checked={selectedMemberIds.has(entry.id)}
              onChange={() => toggleMemberSelect(entry.id)}
            />
          </label>
        ) : (
          <button
            type="button"
            className="member-drag-handle"
            aria-label={t("routing.orderLabel")}
            title={t("routing.reorderHint")}
          >
            <GripVertical size={16} />
          </button>
        )}
        <div className="member-row-main">
          <strong>
            <button
              type="button"
              className="member-channel-link"
              title={t("routing.openChannelModels")}
              onClick={() =>
                navigate(
                  `/models/channel/${candidate.channel.id}?model=${encodeURIComponent(
                    origin || selectedModel,
                  )}`,
                )
              }
            >
              {candidate.channel.name}
            </button>
            {origin ? (
              <span className="member-origin-badge" title={t("routing.memberOriginHint")}>
                {t("routing.memberOrigin", { model: origin })}
              </span>
            ) : null}
          </strong>
          <small>
            #{rowIndex + 1}
            {" · "}
            {t("routing.priorityLabel")}: {entry.priority}
            {" · "}
            {t("routing.weightLabel")}: {entry.weight}
            {(() => {
              const score = evaluation?.score;
              if (score == null || Math.abs(score - entry.weight) < 0.01) {
                return null;
              }
              return (
                <>
                  {" → "}
                  <span className="member-effective-weight" title={t("routing.baseWeightHint")}>
                    {Math.round(score)}
                  </span>
                </>
              );
            })()}
            {entry.manual_override ? (
              <>
                {" "}
                <span className="member-protected" title={t("routing.protectedHint")}>
                  <Shield size={12} /> {t("routing.protectedLabel")}
                </span>
              </>
            ) : null}
            {financeInfo ? (
              (() => {
                const info = financeInfo;
                return (
                  <>
                    {" · "}
                    <span
                      className="member-finance"
                      title={
                        info.overdrawn
                          ? t("routing.financeOverdrawnHint")
                          : t("routing.financeHint")
                      }
                    >
                      {info.overdrawn
                        ? t("routing.financeOverdrawn")
                        : t("routing.financeCalls", { calls: info.calls })}
                      {info.fixed ? t("routing.financeUnitCalls") : t("routing.financeUnitM")}
                    </span>
                  </>
                );
              })()
            ) : (
              <>
                {" · "}
                <span className="member-finance is-na" title={t("routing.financeMissingHint")}>
                  {t("routing.financeMissing")}
                </span>
              </>
            )}
            {entry.fail_count > 0
              ? ` · ${t(activeCooldown ? "routing.failCount" : "routing.failureHistory", {
                  count: entry.fail_count,
                })}`
              : null}
            {activeCooldown && entry.last_error ? ` · ${entry.last_error}` : null}
            {activeCooldown ? (
              <>
                {" "}
                <CooldownHint until={entry.cooldown_until!} />
              </>
            ) : null}
          </small>
        </div>
        <div className="member-controls">
          {route.routing_mode === "single" && route.single_member_id === entry.id ? (
            <span
              className="member-pin-chip"
              title={t("routing.singleModeBanner", { name: candidate.channel.name })}
            >
              <Target size={11} />
              {t("routing.pinChip")}
            </span>
          ) : null}
          <span className="member-row-state">
            <StatusBadge value={state} />
          </span>
          {candidate.channel.status === "auto_disabled" ? (
            <button
              type="button"
              className="member-clear-health"
              title={t("routing.reenableChannelHint")}
              disabled={enableChannel.pendingId === candidate.channel.id}
              onClick={() => enableChannel.mutate(candidate.channel.id)}
            >
              <Power size={13} />
              {t("routing.reenableChannel")}
            </button>
          ) : null}
          {canResetMemberHealth ? (
            <button
              type="button"
              className="member-clear-health"
              title={t(resetActionIsCooldown ? "routing.clearHealth" : "routing.recoverMemberHint")}
              disabled={mutations.clearHealth.isPending}
              onClick={() => mutations.clearHealth.mutate(entry.id)}
            >
              <RotateCcw size={13} />
              {t(resetActionIsCooldown ? "routing.clearHealth" : "routing.recoverMember")}
            </button>
          ) : null}
          <button
            type="button"
            className="icon-button"
            aria-label={t("routing.moveUp")}
            title={t("routing.moveUp")}
            disabled={busy || rowIndex <= 0}
            onClick={() => moveBy(-1)}
          >
            ↑
          </button>
          <button
            type="button"
            className="icon-button"
            aria-label={t("routing.moveDown")}
            title={t("routing.moveDown")}
            disabled={busy || rowIndex >= members.length - 1}
            onClick={() => moveBy(1)}
          >
            ↓
          </button>
          <ActionMenu
            compact
            label={t("common.moreActions")}
            disabled={busy || bulkSelect}
            items={memberActions(entry, {
              t,
              route,
              memberCount: members.length,
              canResetHealth: canResetMemberHealth,
              resetIsCooldown: resetActionIsCooldown,
              mutations,
              setMember,
              setRemoveMember,
            })}
          />
        </div>
      </div>
    );
  });
}
