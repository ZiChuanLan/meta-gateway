import { Link, Navigate, useSearchParams } from "react-router-dom";
import { useEffect, useMemo } from "react";
import { useI18n } from "../i18n";
import { Page, Panel, Tabs } from "../components/ui";
import { Exchange } from "./Exchange";
import { BackupsPanel, RuntimeSettingsPanel } from "./OpsPanels";
import { useModules } from "../hooks/useModules";

type SystemTab = "runtime" | "exchange" | "backups";

/**
 * Settings: runtime, backups, and optional Exchange.
 * Discovery + Audit live under Logs; Check-in is a top-level nav item.
 */
export function Maintain() {
	const { t } = useI18n();
	const [params, setParams] = useSearchParams();
	const modules = useModules();
	const requested = params.get("tab");

	const items = useMemo(() => {
		const next: Array<{ value: SystemTab; label: string }> = [
			{ value: "runtime", label: t("ops.tab.runtime") },
		];
		if (modules.exchangeEnabled) {
			next.push({ value: "exchange", label: t("maintain.tab.exchange") });
		}
		next.push({ value: "backups", label: t("ops.tab.backups") });
		return next;
	}, [modules.exchangeEnabled, t]);

	const addonTabDisabled =
		requested === "exchange" && !modules.exchangeEnabled;

	useEffect(() => {
		if (!modules.ready) return;
		if (!addonTabDisabled) return;
		const next = new URLSearchParams(params);
		next.set("tab", "runtime");
		next.delete("ops");
		setParams(next, { replace: true });
	}, [addonTabDisabled, modules.ready, params, setParams]);

	// Legacy deep-links (after hooks).
	if (requested === "discovery") {
		return <Navigate to="/logs?tab=discovery" replace />;
	}
	if (requested === "audit") {
		return <Navigate to="/logs?tab=audit" replace />;
	}
	if (requested === "checkins") {
		return <Navigate to="/checkins" replace />;
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
				<div className="system-banner">
					<strong>{t("maintain.bannerTitle")}</strong>
					<p>
						{t("maintain.bannerBody")}{" "}
						<Link to="/store">{t("maintain.openStore")}</Link>
					</p>
				</div>
				<Tabs items={items} active={active} onChange={changeTab} />
				{addonTabDisabled ? (
					<Panel>
						<p className="detail-empty">{t("maintain.addonDisabled")}</p>
						<Link className="button button-primary" to="/store">
							{t("maintain.openStore")}
						</Link>
					</Panel>
				) : null}
				{!addonTabDisabled && active === "runtime" ? (
					<RuntimeSettingsPanel />
				) : null}
				{!addonTabDisabled && active === "exchange" && modules.exchangeEnabled ? (
					<div className="maintain-exchange">
						<Exchange embedded />
					</div>
				) : null}
				{!addonTabDisabled && active === "backups" ? <BackupsPanel /> : null}
			</div>
		</Page>
	);
}
