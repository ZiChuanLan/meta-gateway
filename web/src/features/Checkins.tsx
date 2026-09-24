import { useI18n } from "../i18n";
import { Page } from "../components/ui";
import { CheckinsPanel } from "./ops";
import { ExternalCheckinsPanel } from "./ops/ExternalCheckinsPanel";

/**
 * Top-level check-in surface. The check-in feature is built into the gateway,
 * so this page is always reachable — the schedule itself is the only switch,
 * and it lives in the panel below.
 */
export function Checkins() {
	const { t } = useI18n();

	return (
		<Page
			kicker={t("checkinsPage.kicker")}
			title={t("checkinsPage.title")}
			description={t("checkinsPage.description")}
		>
			<div className="ops-canvas">
				<CheckinsPanel>
					<ExternalCheckinsPanel />
				</CheckinsPanel>
			</div>
		</Page>
	);
}
