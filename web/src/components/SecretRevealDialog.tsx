import { Copy } from "lucide-react";

import { useI18n } from "../i18n";
import { Button, Dialog, ErrorState, IconButton } from "./ui";

/**
 * Shared shape for every plaintext-reveal dialog (downstream tokens, rotated
 * tokens, credential secrets): warning banner, then loading / error / the
 * secret with a copy button. The caller owns fetching; this component only
 * renders state.
 */
export function SecretRevealDialog({
	title,
	warning,
	secret,
	pending = false,
	error,
	onRetry,
	closeLabel,
	copyLabel,
	onClose,
}: {
	title: string;
	warning: string;
	secret?: string | null;
	pending?: boolean;
	error?: unknown;
	onRetry?: () => void;
	closeLabel: string;
	copyLabel: string;
	onClose: () => void;
}) {
	const { t } = useI18n();
	return (
		<Dialog
			title={title}
			onClose={onClose}
			actions={
				<Button onClick={onClose} disabled={pending}>
					{closeLabel}
				</Button>
			}
		>
			<p className="warning">{warning}</p>
			{pending ? (
				<p className="exchange-panel-note">{t("common.loading")}</p>
			) : error ? (
				<ErrorState error={error} retry={onRetry} />
			) : secret ? (
				<div className="secret-output">
					<code>{secret}</code>
					<IconButton
						label={copyLabel}
						onClick={() => navigator.clipboard.writeText(secret)}
					>
						<Copy size={14} />
					</IconButton>
				</div>
			) : null}
		</Dialog>
	);
}
