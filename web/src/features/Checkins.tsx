import { Link } from "react-router-dom";
import { useI18n } from "../i18n";
import { Page, Panel } from "../components/ui";
import { CheckinsPanel } from "./ops";
import { ExternalCheckinsPanel } from "./ops/ExternalCheckinsPanel";
import { useModules } from "../hooks/useModules";

/**
 * Top-level Check-in surface (optional Store add-on).
 * Settings no longer hosts this tab.
 */
export function Checkins() {
	const { t } = useI18n();
	const { checkinEnabled, ready } = useModules();

	return (
		<Page
			kicker={t("checkinsPage.kicker")}
			title={t("checkinsPage.title")}
			description={t("checkinsPage.description")}
		>
			<div className="ops-canvas">
				{ready && !checkinEnabled ? (
					<Panel>
						<p className="detail-empty">{t("ops.checkinModuleOff")}</p>
						<Link className="button button-primary" to="/store">
							{t("maintain.openStore")}
						</Link>
					</Panel>
				) : (
					<>
						<CheckinsPanel>
							<ExternalCheckinsPanel />
						</CheckinsPanel>
					</>
				)}
			</div>
		</Page>
	);
}
