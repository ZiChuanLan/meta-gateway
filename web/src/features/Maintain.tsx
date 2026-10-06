import { OperatorClaimPanel } from "./OperatorProfilePanel";
import { UpdateChannelPanel } from "./UpdateChannelPanel";
import { Navigate, useSearchParams } from "react-router-dom";
import { useEffect, useMemo, useState } from "react";
import { useI18n } from "../i18n";
import { Page, Tabs } from "../components/ui";
import { BackupsPanel, RuntimeSettingsPanel } from "./ops";
import { AppearancePanel } from "./AppearancePanel";
import { useOperatingMode } from "../hooks/useOperatingMode";
import { modeHiddenNav } from "../lib/topBar";
import { useSession } from "../session";
import { useUnsavedChanges } from "../lib/unsavedChanges";

type SystemTab = "runtime" | "appearance" | "backups" | "operator" | "updates";

/**
 * Settings: runtime parameters, appearance, and backups. The multi-user area
 * owns its own switch (Users → Overview), so this page only links to it.
 * Discovery + Audit live under Logs; Check-in and Exchange are top-level nav
 * items.
 */
export function Maintain() {
  const { t } = useI18n();
  const { role } = useSession();
  const operatingMode = useOperatingMode();
  const [params, setParams] = useSearchParams();
  const requested = params.get("tab");
  // The runtime draft lives in its panel; this page only needs to know whether
  // switching tabs would throw work away.
  const [runtimeDirty, setRuntimeDirty] = useState(false);
  const guardLeave = useUnsavedChanges(runtimeDirty);

  const items = useMemo<Array<{ value: SystemTab; label: string }>>(
    () => [
      { value: "runtime", label: t("ops.tab.runtime") },
      { value: "appearance", label: t("appearance.title") },
      { value: "backups", label: t("ops.tab.backups") },
      ...(role === null || role === "owner"
        ? [
            { value: "operator" as const, label: t("operator.title") },
            { value: "updates" as const, label: t("updates.channel") },
          ]
        : []),
    ],
    [t, role],
  );

  // Which tab is showing. Computed before the legacy redirects so every hook
  // above them keeps its position in the render order.
  const active: SystemTab = items.some((item) => item.value === requested)
    ? (requested as SystemTab)
    : "runtime";

  // Leaving the runtime tab discards its draft (the panel unmounts), so the
  // pending-change flag must not outlive it — whether the move came from a tab
  // click or from the panel's own link.
  useEffect(() => {
    if (active !== "runtime") setRuntimeDirty(false);
  }, [active]);

  // Legacy deep-links (after hooks).
  if (requested === "mode") {
    return <Navigate to="/settings?tab=runtime" replace />;
  }
  if (requested === "discovery") {
    return <Navigate to="/logs?tab=discovery" replace />;
  }
  if (requested === "audit") {
    return <Navigate to="/logs?tab=audit" replace />;
  }
  if (requested === "checkins") {
    return <Navigate to="/checkins" replace />;
  }
  if (requested === "exchange") {
    return <Navigate to="/exchange" replace />;
  }

  const changeTab = (value: string) => {
    if (value !== active && !guardLeave()) return;
    setRuntimeDirty(false);
    const next = new URLSearchParams(params);
    next.set("tab", value);
    next.delete("ops");
    setParams(next, { replace: true });
  };

  return (
    <Page title={t("maintain.title")} description={t("maintain.description")}>
      <div className="ops-canvas">
        <Tabs items={items} active={active} onChange={changeTab} />
        {active === "runtime" ? <RuntimeSettingsPanel onDirtyChange={setRuntimeDirty} /> : null}
        {active === "appearance" ? (
          <AppearancePanel modeHiddenNav={modeHiddenNav(operatingMode.data?.mode)} />
        ) : null}
        {active === "backups" ? <BackupsPanel /> : null}
        {active === "operator" ? <OperatorClaimPanel /> : null}
        {active === "updates" ? <UpdateChannelPanel /> : null}
      </div>
    </Page>
  );
}
