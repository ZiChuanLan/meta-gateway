import { useMemo, useState } from "react";
import { api } from "../api/client";
import { Button, Field, ErrorState } from "../components/ui";
import { useI18n } from "../i18n";
import { useSession } from "../session";

export type TryUpstreamOption = {
	id: number;
	name: string;
	priority?: number;
	weight?: number;
};

/**
 * Admin console probe: no downstream API key required.
 * Uses admin Bearer → POST /admin/try/chat.
 * Same model name on multiple channels: pick upstream, or leave "auto" for gateway routing.
 */
export function TryPanel({
	defaultModel,
	upstreams = [],
	onClose,
}: {
	defaultModel: string;
	/** Members serving this model (multi-channel same name). */
	upstreams?: TryUpstreamOption[];
	onClose?: () => void;
}) {
	const { t } = useI18n();
	const { client } = useSession();
	const service = api(client!);
	const [model, setModel] = useState(defaultModel);
	const [prompt, setPrompt] = useState("Say hello in one short sentence.");
	const [channelId, setChannelId] = useState<number>(0);
	const [pending, setPending] = useState(false);
	const [error, setError] = useState<unknown>(null);
	const [result, setResult] = useState("");
	const [meta, setMeta] = useState("");

	const orderedUpstreams = useMemo(() => {
		return [...upstreams].sort((a, b) => {
			const pa = a.priority ?? 0;
			const pb = b.priority ?? 0;
			if (pb !== pa) return pb - pa;
			const wa = a.weight ?? 0;
			const wb = b.weight ?? 0;
			if (wb !== wa) return wb - wa;
			return a.name.localeCompare(b.name);
		});
	}, [upstreams]);

	const multiUpstream = orderedUpstreams.length > 1;

	async function run() {
		if (!model.trim()) return;
		setPending(true);
		setError(null);
		setResult("");
		setMeta("");
		try {
			const response = await service.tryChat({
				model: model.trim(),
				prompt,
				channel_id: channelId > 0 ? channelId : undefined,
			});
			const via =
				response.channel_name != null
					? t("try.viaChannel", {
							name: response.channel_name,
							id: response.channel_id ?? "—",
						})
					: t("try.viaAuto");
			setMeta(
				t("try.meta", {
					status: response.status,
					latency: response.latency_ms,
					model: response.model,
					via,
				}),
			);
			setResult(JSON.stringify(response.body, null, 2));
			if (response.status < 200 || response.status >= 300) {
				setError(
					new Error(t("try.upstreamStatus", { status: response.status })),
				);
			}
		} catch (err) {
			setError(err);
		} finally {
			setPending(false);
		}
	}

	return (
		<div className="try-box">
			<p className="muted" style={{ marginBottom: 12, fontSize: 13 }}>
				{multiUpstream ? t("try.hintMulti") : t("try.hint")}
			</p>
			<Field label={t("common.model")}>
				<input
					value={model}
					onChange={(e) => setModel(e.target.value)}
					className="mono"
				/>
			</Field>
			{orderedUpstreams.length > 0 ? (
				<Field
					label={t("try.upstream")}
					hint={
						multiUpstream ? t("try.upstreamHintMulti") : t("try.upstreamHintOne")
					}
				>
					<select
						value={channelId}
						onChange={(e) => setChannelId(Number(e.target.value) || 0)}
					>
						<option value={0}>{t("try.upstreamAuto")}</option>
						{orderedUpstreams.map((upstream) => (
							<option key={upstream.id} value={upstream.id}>
								{upstream.name}
								{upstream.priority != null
									? ` · p${upstream.priority}/w${upstream.weight ?? 0}`
									: ""}
							</option>
						))}
					</select>
				</Field>
			) : null}
			<Field label={t("try.prompt")}>
				<textarea value={prompt} onChange={(e) => setPrompt(e.target.value)} />
			</Field>
			{error ? <ErrorState error={error} /> : null}
			{meta ? <div className="result-strip result-strip-info">{meta}</div> : null}
			{result ? <pre className="try-result">{result}</pre> : null}
			<div className="detail-actions" style={{ marginTop: 12 }}>
				{onClose ? (
					<Button variant="secondary" onClick={onClose}>
						{t("common.close")}
					</Button>
				) : null}
				<Button disabled={pending || !model.trim()} onClick={() => void run()}>
					{pending ? t("common.working") : t("try.send")}
				</Button>
			</div>
		</div>
	);
}
