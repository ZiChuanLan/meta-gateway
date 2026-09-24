import { useI18n } from "../../i18n";
import type { PluginHookStatus } from "../../api/types";

/**
 * The intercept hooks a plugin currently serves.
 *
 * This is the one place where a plugin sees traffic it does not own: a route
 * hook decides which model answers, a request hook rewrites what goes
 * upstream, a response hook rewrites what comes back. The readout therefore
 * lives on the plugin page rather than inside a config dialog, and it states
 * the trust boundary in plain words instead of implying it with an icon.
 *
 * Renders nothing when no plugin declares a hook — an empty section would
 * suggest the capability is missing rather than unused.
 */
export function HookSection({ hooks }: { hooks: PluginHookStatus[] }) {
	const { t } = useI18n();
	if (hooks.length === 0) return null;
	// Unknown points render as their raw name: a newer backend must not be able
	// to make the console print a translation key at the operator.
	const pointLabel = (point: string) => {
		switch (point) {
			case "route":
				return t("store.hook.point.route");
			case "request":
				return t("store.hook.point.request");
			case "response":
				return t("store.hook.point.response");
			default:
				return point;
		}
	};
	return (
		<section className="store-section">
			<header className="store-section-head">
				<h2>{t("store.hooks")}</h2>
				<p>{t("store.hooksHint")}</p>
			</header>
			<div className="hook-rows">
				{hooks.map((hook) => (
					<div className="hook-row" key={`${hook.plugin_id}:${hook.point}:${hook.path}`}>
						<div className="hook-row-head">
							<span className={`hook-point is-${hook.point}`}>{pointLabel(hook.point)}</span>
							<span className="hook-plugin">{hook.plugin_name || hook.plugin_id}</span>
							{hook.tripped ? <span className="hook-tripped">{t("store.hookTripped")}</span> : null}
						</div>
						<div className="hook-row-meta">
							<code>{hook.match_models.join(", ")}</code>
							<span>{t("store.hookTimeout", { ms: hook.timeout_ms })}</span>
						</div>
					</div>
				))}
			</div>
			<p className="hook-warning">{t("store.hooksWarning")}</p>
		</section>
	);
}
