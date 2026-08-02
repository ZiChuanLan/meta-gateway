import { Link } from "react-router-dom";
import { useI18n } from "../i18n";
import { Page, Panel } from "../components/ui";
import { Exchange } from "./Exchange";
import { useModules } from "../hooks/useModules";

/**
 * Top-level Exchange surface (optional Store add-on).
 * Settings no longer hosts this tab; Exchange renders its own Page shell.
 */
export function ExchangePage() {
	const { t } = useI18n();
	const { exchangeEnabled, ready } = useModules();

	if (ready && !exchangeEnabled) {
		return (
			<Page
				kicker={t("maintain.kicker")}
				title={t("exchange.title")}
				description={t("exchange.description")}
			>
				<div className="ops-canvas">
					<Panel>
						<p className="detail-empty">{t("maintain.addonDisabled")}</p>
						<Link className="button button-primary" to="/store">
							{t("maintain.openStore")}
						</Link>
					</Panel>
				</div>
			</Page>
		);
	}
	return <Exchange />;
}
