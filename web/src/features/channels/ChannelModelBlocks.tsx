import { useQuery, useQueryClient } from "@tanstack/react-query"
import { api } from "../../api/client"
import type {  } from "../../api/types"
import { useI18n } from "../../i18n"
import { useSession } from "../../session"

export // Channel × model not-found blacklist: entries auto-created when the
// upstream reports a model as unknown; cleared manually here.
function ChannelModelBlocks({ channelId }: { channelId: number }) {
	const { client } = useSession();
	const service = api(client!);
	const { t } = useI18n();
	const query = useQuery({
		queryKey: ["model-blocks"],
		queryFn: ({ signal }) => service.modelBlocks(signal),
		refetchInterval: 30_000,
	});
	const queryClient = useQueryClient();
	const unblock = (model: string) =>
		service.unblockModel(channelId, model).then(() => {
			queryClient.invalidateQueries({ queryKey: ["model-blocks"] });
		});
	const blocks = (query.data?.items ?? []).filter(
		(block) => block.channel_id === channelId,
	);
	if (!blocks.length) return null;
	return (
		<div className="detail-pricing">
			<span className="label is-warn">{t("channels.modelBlocks")}</span>
			<div className="detail-pricing-list">
				{blocks.map((block) => (
					<div key={block.id} className="detail-pricing-row is-blocked">
						<code>{block.model}</code>
						<button
							type="button"
							className="model-block-clear"
							title={t("channels.modelBlockClear")}
							onClick={() => void unblock(block.model)}
						>
							{t("channels.modelBlockClear")}
						</button>
					</div>
				))}
			</div>
		</div>
	);
}
