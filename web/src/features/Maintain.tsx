import { useSearchParams } from "react-router-dom";
import { useI18n } from "../i18n";
import { Page, Tabs } from "../components/ui";
import { Exchange } from "./Exchange";
import {
	AuditPanel,
	BackupsPanel,
	CheckinsPanel,
	DiscoveryPanel,
	RuntimeSettingsPanel,
} from "./OpsPanels";

type SystemTab =
	| "runtime"
	| "discovery"
	| "checkins"
	| "exchange"
	| "audit"
	| "backups";

/**
 * Low-frequency settings tools. Daily loop stays on
 * Connections → Models → Tokens → Logs.
 */
export function Maintain() {
	const { t } = useI18n();
	const [params, setParams] = useSearchParams();
	const requested = params.get("tab");
	const items: Array<{ value: SystemTab; label: string }> = [
		{ value: "runtime", label: t("ops.tab.runtime") },
		{ value: "discovery", label: t("ops.tab.discovery") },
		{ value: "checkins", label: t("ops.tab.checkins") },
		{ value: "exchange", label: t("maintain.tab.exchange") },
		{ value: "audit", label: t("ops.tab.audit") },
		{ value: "backups", label: t("ops.tab.backups") },
	];
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
					<p>{t("maintain.bannerBody")}</p>
				</div>
				<Tabs items={items} active={active} onChange={changeTab} />
				{active === "runtime" ? <RuntimeSettingsPanel /> : null}
				{active === "discovery" ? <DiscoveryPanel /> : null}
				{active === "checkins" ? <CheckinsPanel /> : null}
				{active === "exchange" ? (
					<div className="maintain-exchange">
						<Exchange embedded />
					</div>
				) : null}
				{active === "audit" ? <AuditPanel /> : null}
				{active === "backups" ? <BackupsPanel /> : null}
			</div>
		</Page>
	);
}
