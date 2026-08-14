import { useParams } from "react-router-dom";
import { useState } from "react";
import { useI18n } from "../i18n";
import { useSession } from "../session";

/**
 * PluginHost embeds a registered sidecar plugin's page in an iframe inside
 * the gateway shell — the sidebar stays visible so the plugin is a first-class
 * nav destination (like check-in / exchange), and the iframe fills the whole
 * content area. The iframe points at the admin proxy endpoint with the
 * session token as ?t= — the backend accepts it as the Authorization
 * equivalent so a plain iframe (which cannot send headers) authenticates.
 */
export function PluginHost() {
	const { id } = useParams<{ id: string }>();
	const { t } = useI18n();
	const { client } = useSession();
	const [error, setError] = useState(false);
	const token = client?.getToken() ?? "";

	if (!id) return null;
	const src = `/admin/plugins/${encodeURIComponent(id)}/proxy/?t=${encodeURIComponent(token)}`;

	return (
		<main className="page plugin-page">
			<div className="plugin-host">
				{error ? (
					<p className="is-quiet" style={{ fontSize: 13 }}>
						{t("plugins.loadFailed")}
					</p>
				) : (
					<iframe
						title={t("plugins.title")}
						src={src}
						onError={() => setError(true)}
						className="plugin-host-frame"
					/>
				)}
			</div>
		</main>
	);
}

export default PluginHost;
