import { OperatorProfilePanel } from "./OperatorProfilePanel";
import { UpdateChannelPanel } from "./UpdateChannelPanel";
import { Navigate, useSearchParams } from "react-router-dom";
import { useMemo } from "react";
import { useI18n } from "../i18n";
import { Page, Tabs } from "../components/ui";
import { BackupsPanel, RuntimeSettingsPanel } from "./ops";
import { AppearancePanel } from "./AppearancePanel";
import { useOperatingMode } from "../hooks/useOperatingMode";
import { modeHiddenNav } from "../lib/topBar";

type SystemTab = "runtime" | "appearance" | "backups" | "operator" | "updates";

/**
 * Settings: runtime parameters, appearance, and backups. The multi-user area
 * owns its own switch (Users → Overview), so this page only links to it.
 * Discovery + Audit live under Logs; Check-in and Exchange are top-level nav
 * items.
 */
export function Maintain() {
	const { t } = useI18n();
	const operatingMode = useOperatingMode();
	const [params, setParams] = useSearchParams();
	const requested = params.get("tab");

	const items = useMemo<Array<{ value: SystemTab; label: string }>>(() => [
		{ value: "runtime", label: t("ops.tab.runtime") },
		{ value: "appearance", label: t("appearance.title") },
		{ value: "backups", label: t("ops.tab.backups") },
 {value:"operator",label:t("operator.title")},
 {value:"updates",label:t("updates.channel")},
	], [t]);

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

	const active: SystemTab = items.some((item) => item.value === requested)
		? (requested as SystemTab)
		: "runtime";

	const changeTab = (value: string) => {
		const next = new URLSearchParams(params);
		next.set("tab", value);
		next.delete("ops");
		setParams(next, { replace: true });
	};

	return (
		<Page
			kicker={t("maintain.kicker")}
			title={t("maintain.title")}
			description={t("maintain.description")}
		>
			<div className="ops-canvas">
				<Tabs items={items} active={active} onChange={changeTab} />
				{active === "runtime" ? <RuntimeSettingsPanel /> : null}
				{active === "appearance" ? (
					<AppearancePanel modeHiddenNav={modeHiddenNav(operatingMode.data?.mode)} />
				) : null}
				{active === "backups" ? <BackupsPanel /> : null}
 {active === "operator" ? <OperatorProfilePanel/> : null}
 {active === "updates" ? <UpdateChannelPanel/> : null}
			</div>
		</Page>
	);
}
