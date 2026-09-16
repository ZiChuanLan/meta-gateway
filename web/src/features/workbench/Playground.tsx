import { useEffect, useMemo, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import {
	Copy,
	Eraser,
	Pencil,
	RefreshCw,
	Send,
	Square,
	Trash2,
} from "lucide-react";
import { api } from "../../api/client";
import {
	Button,
	Empty,
	ErrorState,
	Field,
	IconButton,
	Panel,
	formatDate,
} from "../../components/ui";
import { useI18n } from "../../i18n";
import { parseSseJson, splitSseFrames } from "../../lib/sse";
import { useSession } from "../../session";

/**
 * The workbench's chat playground. It talks to the same admin probe the Models
 * page opens on a single route, but keeps the conversation: the probe endpoint
 * accepts the whole transcript, so multi-turn, streaming and per-turn actions
 * all live here rather than in a bigger one-shot form.
 */
const CHAT_ENDPOINT = "/v1/chat/completions";
const SUGGESTIONS = ["playground.suggest1", "playground.suggest2", "playground.suggest3"];

type Role = "user" | "assistant";
type TurnStatus = "streaming" | "complete" | "error";

type Turn = {
	key: string;
	role: Role;
	content: string;
	reasoning: string;
	status: TurnStatus;
	at: string;
	error?: string;
	stopped?: boolean;
	editDraft?: string;
	latencyMs?: number;
	upstreamStatus?: number;
	channelName?: string;
	tokens?: number;
};

type ChatTurn = { role: string; content: string };

let turnSeq = 0;
const uid = () => `turn-${++turnSeq}`;

function textOf(value: unknown): string {
	if (typeof value === "string") return value;
	if (Array.isArray(value)) {
		return value
			.map((part) =>
				typeof part === "string"
					? part
					: ((part as { text?: unknown })?.text as string) ?? "",
			)
			.filter(Boolean)
			.join("\n");
	}
	return "";
}

type Holder = Record<string, unknown>;

function readParts(holder: unknown): { content: string; reasoning: string } {
	if (!holder || typeof holder !== "object") return { content: "", reasoning: "" };
	const source = holder as Holder;
	return {
		content: textOf(source.content),
		reasoning: textOf(source.reasoning_content ?? source.reasoning),
	};
}

function firstChoice(body: unknown): { delta?: unknown; message?: unknown } | null {
	const choices = (body as { choices?: unknown } | null)?.choices;
	if (!Array.isArray(choices) || choices.length === 0) return null;
	const choice = choices[0] as { delta?: unknown; message?: unknown };
	return choice && typeof choice === "object" ? choice : null;
}

/** Providers wrap refusals differently; show whatever they actually said. */
function upstreamMessage(body: unknown): string {
	if (typeof body === "string") return body.trim().slice(0, 400);
	if (!body || typeof body !== "object") return "";
	const error = (body as Holder).error;
	if (typeof error === "string") return error;
	if (error && typeof error === "object") {
		const message = (error as Holder).message;
		if (typeof message === "string") return message;
	}
	return JSON.stringify(body).slice(0, 400);
}

export default function Playground({ active }: { active: boolean }) {
	const { t } = useI18n();
	const { client } = useSession();
	const service = api(client!);

	const [model, setModel] = useState("");
	const [channelId, setChannelId] = useState(0);
	const [turns, setTurns] = useState<Turn[]>([]);
	const [draft, setDraft] = useState("");
	const [busy, setBusy] = useState(false);
	const [copied, setCopied] = useState("");
	const [system, setSystem] = useState("");
	const [maxTokens, setMaxTokens] = useState(4096);
	const [temperature, setTemperature] = useState(0.7);
	const [temperatureOn, setTemperatureOn] = useState(true);
	const [topP, setTopP] = useState(1);
	const [topPOn, setTopPOn] = useState(false);
	const [stream, setStream] = useState(true);
	const abortRef = useRef<AbortController | null>(null);
	const scroller = useRef<HTMLDivElement>(null);

	const routes = useQuery({
		queryKey: ["route-overviews"],
		queryFn: ({ signal }) => service.routeOverviews(signal),
		enabled: active,
	});
	// Wildcard patterns are route matchers, not callable model names.
	const modelNames = useMemo(
		() =>
			Array.from(
				new Set(
					(routes.data ?? [])
						.filter(({ route }) => route.enabled && !/[*?]/.test(route.model_pattern))
						.map(({ route }) => route.model_pattern),
				),
			).sort(),
		[routes.data],
	);
	const capabilities = useQuery({
		queryKey: ["capabilities", modelNames],
		queryFn: () => service.resolveModelCapabilities(modelNames),
		enabled: active && modelNames.length > 0,
	});
	// Image and embedding registrations answer elsewhere; a chat turn to them
	// would only produce a request the upstream has no route for.
	const chatModels = useMemo(
		() =>
			modelNames.filter((name) =>
				(capabilities.data?.items[name]?.endpoints ?? [CHAT_ENDPOINT]).includes(
					CHAT_ENDPOINT,
				),
			),
		[modelNames, capabilities.data],
	);
	const activeModel = chatModels.includes(model) ? model : (chatModels[0] ?? "");
	const upstreams = useMemo(() => {
		const overview = (routes.data ?? []).find(
			({ route }) => route.model_pattern === activeModel,
		);
		return (overview?.members ?? [])
			.map((candidate) => ({
				id: candidate.channel.id,
				name: candidate.channel.name,
				priority: candidate.member.priority,
				weight: candidate.member.weight,
			}))
			.sort((left, right) => (right.priority ?? 0) - (left.priority ?? 0));
	}, [routes.data, activeModel]);

	useEffect(() => {
		const node = scroller.current;
		if (node) node.scrollTop = node.scrollHeight;
	}, [turns]);
	useEffect(() => () => abortRef.current?.abort(), []);

	function patch(key: string, update: (turn: Turn) => Turn) {
		setTurns((current) =>
			current.map((turn) => (turn.key === key ? update(turn) : turn)),
		);
	}

	/** Runs one assistant turn against the gateway and fills the placeholder. */
	async function run(history: ChatTurn[], assistantKey: string) {
		abortRef.current?.abort();
		const controller = new AbortController();
		abortRef.current = controller;
		setBusy(true);
		const startedAt = performance.now();
		const elapsed = () => Math.round(performance.now() - startedAt);
		const request = {
			model: activeModel,
			messages: history,
			system: system.trim() || undefined,
			max_tokens: maxTokens,
			temperature: temperatureOn ? temperature : undefined,
			top_p: topPOn ? topP : undefined,
			channel_id: channelId > 0 ? channelId : undefined,
		};

		try {
			if (!stream) {
				const response = await service.tryChat(request);
				const { content, reasoning } = readParts(firstChoice(response.body)?.message);
				const failed = response.status < 200 || response.status >= 300;
				patch(assistantKey, (turn) => ({
					...turn,
					content: content || turn.content,
					reasoning,
					upstreamStatus: response.status,
					latencyMs: response.latency_ms ?? elapsed(),
					channelName: response.channel_name,
					status: failed ? "error" : content ? "complete" : "error",
					error: failed
						? upstreamMessage(response.body) ||
							t("playground.upstreamError", { status: response.status })
						: content
							? undefined
							: t("playground.noContent", { status: response.status }),
				}));
				return;
			}

			const response = await service.streamTryChat(request, controller.signal);
			const reader = response.body?.getReader();
			if (!reader) throw new Error(t("playground.unreadable"));
			const decoder = new TextDecoder();
			let buffer = "";
			for (;;) {
				const chunk = await reader.read();
				if (chunk.done) break;
				buffer += decoder.decode(chunk.value, { stream: true });
				const { frames, rest } = splitSseFrames(buffer);
				buffer = rest;
				for (const frame of frames) {
					if (frame.event === "meta") {
						const meta = parseSseJson<{
							status?: number;
							latency_ms?: number;
							channel_name?: string;
						}>(frame.data);
						if (meta) {
							patch(assistantKey, (turn) => ({
								...turn,
								upstreamStatus: meta.status,
								latencyMs: meta.latency_ms,
								channelName: meta.channel_name,
							}));
						}
						continue;
					}
					if (frame.event === "error") {
						const body = parseSseJson(frame.data);
						patch(assistantKey, (turn) => ({
							...turn,
							status: "error",
							error:
								upstreamMessage(body) ||
								frame.data.trim().slice(0, 400) ||
								t("playground.upstreamError", { status: turn.upstreamStatus ?? 0 }),
						}));
						continue;
					}
					if (frame.event === "raw") {
						// A channel stream policy can override the stream away; the
						// proxy then hands back one aggregated completion.
						const body = parseSseJson(frame.data);
						const { content, reasoning } = readParts(firstChoice(body)?.message);
						patch(assistantKey, (turn) => ({
							...turn,
							content: content || turn.content || frame.data.trim().slice(0, 2000),
							reasoning: reasoning || turn.reasoning,
						}));
						continue;
					}
					if (frame.event === "done") continue;
					// Everything else is the provider's own frame, relayed verbatim.
					const body = parseSseJson(frame.data);
					const choice = body ? firstChoice(body) : null;
					if (!choice) continue;
					const { content, reasoning } = readParts(choice.delta ?? choice.message);
					if (!content && !reasoning) continue;
					patch(assistantKey, (turn) => ({
						...turn,
						content: turn.content + content,
						reasoning: turn.reasoning + reasoning,
					}));
				}
			}
			patch(assistantKey, (turn) =>
				turn.status === "streaming"
					? { ...turn, status: "complete", latencyMs: turn.latencyMs ?? elapsed() }
					: turn,
			);
			patch(assistantKey, (turn) =>
				turn.status === "complete" && !turn.content && !turn.error
					? {
							...turn,
							status: "error",
							error: t("playground.noContent", { status: turn.upstreamStatus ?? 0 }),
						}
					: turn,
			);
		} catch (failure) {
			if (controller.signal.aborted) {
				// Stop keeps whatever already arrived — that is the point of stop.
				patch(assistantKey, (turn) => ({
					...turn,
					status: "complete",
					stopped: true,
					latencyMs: turn.latencyMs ?? elapsed(),
				}));
				return;
			}
			patch(assistantKey, (turn) => ({
				...turn,
				status: "error",
				error: failure instanceof Error ? failure.message : String(failure),
			}));
		} finally {
			if (abortRef.current === controller) abortRef.current = null;
			setBusy(false);
		}
	}

	const transcript = (list: Turn[]): ChatTurn[] =>
		list
			.filter((turn) => turn.status !== "error" && turn.content.trim())
			.map(({ role, content }) => ({ role, content }));

	function send(text: string = draft) {
		const prompt = text.trim();
		if (!prompt || busy || !activeModel) return;
		setDraft("");
		const user: Turn = {
			key: uid(),
			role: "user",
			content: prompt,
			reasoning: "",
			status: "complete",
			at: new Date().toISOString(),
		};
		const assistant: Turn = {
			key: uid(),
			role: "assistant",
			content: "",
			reasoning: "",
			status: "streaming",
			at: new Date().toISOString(),
		};
		const history = transcript(turns);
		setTurns((current) => [...current, user, assistant]);
		void run([...history, { role: "user", content: prompt }], assistant.key);
	}

	function regenerate(key: string) {
		if (busy) return;
		const index = turns.findIndex((turn) => turn.key === key);
		if (index < 0) return;
		const history = transcript(turns.slice(0, index));
		const fresh: Turn = {
			key,
			role: "assistant",
			content: "",
			reasoning: "",
			status: "streaming",
			at: new Date().toISOString(),
		};
		setTurns((current) =>
			current.map((turn, position) => (position === index ? fresh : turn)),
		);
		void run(history, key);
	}

	/** Applies an edit and re-runs the turn — the "save and submit" path. */
	function resubmit(key: string, content: string) {
		if (busy) return;
		const index = turns.findIndex((turn) => turn.key === key);
		const target = turns[index];
		if (index < 0 || !target) return;
		const edited: Turn = {
			...target,
			content,
			editDraft: undefined,
			status: "complete",
			error: undefined,
		};
		const history = transcript([...turns.slice(0, index), edited]);
		const assistant: Turn = {
			key: uid(),
			role: "assistant",
			content: "",
			reasoning: "",
			status: "streaming",
			at: new Date().toISOString(),
		};
		setTurns((current) => [...current.slice(0, index), edited, assistant]);
		void run(history, assistant.key);
	}

	function remove(key: string) {
		if (busy) return;
		const index = turns.findIndex((turn) => turn.key === key);
		const target = turns[index];
		if (index < 0 || !target) return;
		// A user turn owns the reply under it; dropping one alone would leave an
		// answer with no question.
		const owned = target.role === "user" && turns[index + 1]?.role === "assistant";
		setTurns((current) =>
			current.filter(
				(_, position) => position !== index && !(owned && position === index + 1),
			),
		);
	}

	async function copy(text: string, key: string) {
		try {
			await navigator.clipboard.writeText(text);
			setCopied(key);
			window.setTimeout(
				() => setCopied((current) => (current === key ? "" : current)),
				1600,
			);
		} catch {
			/* Clipboard permission is not worth an error banner. */
		}
	}

	function clear() {
		abortRef.current?.abort();
		setTurns([]);
		setDraft("");
	}

	if (routes.isPending || (modelNames.length > 0 && capabilities.isPending)) {
		return (
			<p className="muted" role="status">
				{t("common.working")}
			</p>
		);
	}
	if (routes.isError) return <ErrorState error={routes.error} />;
	if (modelNames.length > 0 && capabilities.isError) {
		return <ErrorState error={capabilities.error} />;
	}
	if (chatModels.length === 0) {
		return (
			<Panel>
				<Empty>{t("playground.noModels")}</Empty>
			</Panel>
		);
	}

	const canSend = !!activeModel && !!draft.trim() && !busy;

	return (
		<Panel
			title={t("playground.title")}
			titleHelp={t("playground.help")}
			actions={
				<Button
					variant="secondary"
					icon={<Eraser size={14} />}
					disabled={busy || turns.length === 0}
					onClick={clear}
				>
					{t("playground.clear")}
				</Button>
			}
		>
			<p className="panel-hint">{t("playground.desc")}</p>
			<div className="meta-form pg-pickers">
				<Field label={t("playground.model")} hint={t("playground.modelHint")}>
					<select
						aria-label={t("playground.model")}
						value={activeModel}
						onChange={(event) => {
							setModel(event.target.value);
							// Pinned upstreams belong to the previous model's members.
							setChannelId(0);
						}}
					>
						{chatModels.map((name) => (
							<option key={name} value={name}>
								{name}
							</option>
						))}
					</select>
				</Field>
				<Field
					label={t("playground.upstream")}
					hint={
						upstreams.length > 1
							? t("playground.upstreamHint")
							: t("playground.upstreamHintOne")
					}
				>
					<select
						aria-label={t("playground.upstream")}
						value={channelId}
						onChange={(event) => setChannelId(Number(event.target.value) || 0)}
					>
						<option value={0}>{t("playground.upstreamAuto")}</option>
						{upstreams.map((upstream) => (
							<option key={upstream.id} value={upstream.id}>
								{upstream.name} · p{upstream.priority ?? 0}/w{upstream.weight ?? 0}
							</option>
						))}
					</select>
				</Field>
			</div>

			<details className="pg-params">
				<summary>{t("playground.params")}</summary>
				<p className="panel-hint">{t("playground.paramsHint")}</p>
				<div className="meta-form">
					<Field label={t("playground.system")}>
						<textarea
							rows={2}
							value={system}
							placeholder={t("playground.systemPlaceholder")}
							onChange={(event) => setSystem(event.target.value)}
						/>
					</Field>
					<Field label={t("playground.maxTokens")} hint={t("playground.maxTokensHint")}>
						<input
							type="number"
							min={1}
							value={maxTokens}
							onChange={(event) =>
								setMaxTokens(Math.max(1, Number(event.target.value) || 1))
							}
						/>
					</Field>
					<Field label={t("playground.temperature")}>
						<label className="check marginless">
							<input
								type="checkbox"
								checked={temperatureOn}
								onChange={(event) => setTemperatureOn(event.target.checked)}
							/>
							<span>{t("playground.useParam")}</span>
						</label>
						<input
							type="number"
							step="0.1"
							min={0}
							max={2}
							disabled={!temperatureOn}
							value={temperature}
							onChange={(event) => setTemperature(Number(event.target.value) || 0)}
						/>
					</Field>
					<Field label={t("playground.topP")}>
						<label className="check marginless">
							<input
								type="checkbox"
								checked={topPOn}
								onChange={(event) => setTopPOn(event.target.checked)}
							/>
							<span>{t("playground.useParam")}</span>
						</label>
						<input
							type="number"
							step="0.05"
							min={0}
							max={1}
							disabled={!topPOn}
							value={topP}
							onChange={(event) => setTopP(Number(event.target.value) || 0)}
						/>
					</Field>
					<Field label={t("playground.stream")} hint={t("playground.streamHint")}>
						<label className="check marginless">
							<input
								type="checkbox"
								checked={stream}
								onChange={(event) => setStream(event.target.checked)}
							/>
							<span>{t("playground.streamEnable")}</span>
						</label>
					</Field>
				</div>
			</details>

			<div className="pg-thread" ref={scroller}>
				{turns.length === 0 ? (
					<div className="pg-empty">
						<p className="muted">{t("playground.empty")}</p>
						<div className="pg-suggestions">
							{SUGGESTIONS.map((key) => (
								<button
									type="button"
									key={key}
									className="pg-suggestion"
									onClick={() => setDraft(t(key))}
								>
									{t(key)}
								</button>
							))}
						</div>
					</div>
				) : (
					turns.map((turn) => (
						<TurnView
							key={turn.key}
							turn={turn}
							busy={busy}
							copied={copied === turn.key}
							onCopy={() => void copy(turn.content, turn.key)}
							onEdit={(value) =>
								patch(turn.key, (current) => ({ ...current, editDraft: value }))
							}
							onEditCancel={() =>
								patch(turn.key, (current) => ({ ...current, editDraft: undefined }))
							}
							onEditSave={(value) =>
								patch(turn.key, (current) => ({
									...current,
									content: value,
									editDraft: undefined,
								}))
							}
							onEditSubmit={(value) => resubmit(turn.key, value)}
							onRegenerate={() => regenerate(turn.key)}
							onDelete={() => remove(turn.key)}
						/>
					))
				)}
			</div>

			<div className="pg-composer">
				<textarea
					rows={3}
					value={draft}
					placeholder={t("playground.promptPlaceholder")}
					onChange={(event) => setDraft(event.target.value)}
					onKeyDown={(event) => {
						if (event.key === "Enter" && (event.metaKey || event.ctrlKey)) {
							event.preventDefault();
							send();
						}
					}}
				/>
				<div className="pg-composer-actions">
					<span className="muted">{t("playground.promptHint")}</span>
					<span className="flex-spacer" />
					{busy ? (
						<Button
							variant="secondary"
							icon={<Square size={14} />}
							onClick={() => abortRef.current?.abort()}
						>
							{t("playground.stop")}
						</Button>
					) : null}
					<Button
						disabled={!canSend}
						icon={<Send size={14} />}
						onClick={() => send()}
					>
						{t("playground.send")}
					</Button>
				</div>
			</div>
		</Panel>
	);
}

function TurnView({
	turn,
	busy,
	copied,
	onCopy,
	onEdit,
	onEditCancel,
	onEditSave,
	onEditSubmit,
	onRegenerate,
	onDelete,
}: {
	turn: Turn;
	busy: boolean;
	copied: boolean;
	onCopy: () => void;
	onEdit: (value: string) => void;
	onEditCancel: () => void;
	onEditSave: (value: string) => void;
	onEditSubmit: (value: string) => void;
	onRegenerate: () => void;
	onDelete: () => void;
}) {
	const { t } = useI18n();
	const editing = turn.editDraft !== undefined;
	const draft = turn.editDraft ?? "";

	return (
		<article className={`pg-turn pg-turn-${turn.role}`} data-status={turn.status}>
			<header className="pg-turn-head">
				<strong>
					{t(turn.role === "user" ? "playground.you" : "playground.assistant")}
				</strong>
				{turn.upstreamStatus ? (
					<span className={turn.status === "error" ? "pg-chip is-danger" : "pg-chip"}>
						{turn.upstreamStatus}
					</span>
				) : null}
				{turn.latencyMs != null ? (
					<span className="pg-chip">{t("playground.latency", { ms: turn.latencyMs })}</span>
				) : null}
				{turn.channelName ? <span className="pg-chip">{turn.channelName}</span> : null}
				{turn.stopped ? <span className="pg-chip">{t("playground.stopped")}</span> : null}
				{/* Only the newest turn is worth a timestamp; the rest is noise. */}
				<span className="pg-turn-time muted">{formatDate(turn.at)}</span>
			</header>

			{editing ? (
				<div className="pg-editor">
					<textarea
						rows={4}
						value={draft}
						aria-label={t("playground.edit")}
						onChange={(event) => onEdit(event.target.value)}
					/>
					<div className="pg-turn-actions">
						<Button variant="secondary" onClick={onEditCancel}>
							{t("common.cancel")}
						</Button>
						<Button variant="secondary" onClick={() => onEditSave(draft)}>
							{t("playground.save")}
						</Button>
						<Button
							disabled={busy || !draft.trim()}
							onClick={() => onEditSubmit(draft)}
						>
							{t("playground.saveAndSubmit")}
						</Button>
					</div>
				</div>
			) : (
				<>
					{turn.reasoning ? (
						<details className="pg-reasoning">
							<summary>{t("playground.reasoning")}</summary>
							<pre>{turn.reasoning}</pre>
						</details>
					) : null}
					{turn.content ? <pre className="pg-content">{turn.content}</pre> : null}
					{turn.status === "streaming" && !turn.content ? (
						<p className="muted" role="status">
							{t("common.working")}
						</p>
					) : null}
					{turn.error ? (
						<p className="pg-error" role="alert">
							{turn.error}
						</p>
					) : null}
					<div className="pg-turn-actions">
						<IconButton
							label={copied ? t("playground.copied") : t("playground.copy")}
							disabled={!turn.content}
							onClick={onCopy}
						>
							<Copy size={13} />
						</IconButton>
						<IconButton
							label={t("playground.edit")}
							disabled={busy}
							onClick={() => onEdit(turn.content)}
						>
							<Pencil size={13} />
						</IconButton>
						{turn.role === "assistant" ? (
							<IconButton
								label={t("playground.regenerate")}
								disabled={busy}
								onClick={onRegenerate}
							>
								<RefreshCw size={13} />
							</IconButton>
						) : null}
						<IconButton
							label={t("playground.delete")}
							disabled={busy}
							onClick={onDelete}
						>
							<Trash2 size={13} />
						</IconButton>
					</div>
				</>
			)}
		</article>
	);
}
