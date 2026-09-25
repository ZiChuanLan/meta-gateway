/**
 * What the workbench keeps between visits.
 *
 * A generation costs real upstream quota and the images are the only evidence
 * of what was produced, so both tabs survive a reload: the image runs (with
 * their full-resolution payloads) and the playground transcript. Everything
 * lives in the console store (`lib/consoleStore`), which prefers IndexedDB and
 * degrades to localStorage / memory.
 *
 * Caps are deliberate: the store is per-browser and shared with nothing else,
 * but an unbounded history of base64 images would grow without limit. Trimming
 * always drops the OLDEST entry — never the run the operator just made.
 */
import { readConsoleState, writeConsoleState } from "../../lib/consoleStore";

export interface WorkbenchImage {
	data_url?: string;
	url?: string;
	revised_prompt?: string;
}

/** One generation attempt, success or refusal. */
export interface WorkbenchRun {
	id: string;
	at: string;
	model: string;
	status: number;
	latencyMs: number;
	channelName?: string;
	endpoint: string;
	format: string;
  /** The request, kept so a run can be repeated without retyping it. */
  prompt: string;
  mode: string;
  size?: string;
  images: WorkbenchImage[];
	upstreamError?: string;
}

/** The request form as the operator left it. */
export interface ImageFormState {
	model: string;
	mode: string;
	size: string;
	prompt: string;
}

export type StoredTurnStatus = "streaming" | "complete" | "error";

/** One playground message. `editDraft` is UI-only and never stored. */
export interface StoredTurn {
	key: string;
	role: "user" | "assistant";
	content: string;
	reasoning: string;
	status: StoredTurnStatus;
	at: string;
	error?: string;
	stopped?: boolean;
	latencyMs?: number;
	upstreamStatus?: number;
	channelName?: string;
}

export interface PlaygroundSession {
	model: string;
	memberId: number;
	system: string;
	maxTokens: number;
	temperature: number;
	temperatureOn: boolean;
	topP: number;
	topPOn: boolean;
	stream: boolean;
	turns: StoredTurn[];
}

export const MAX_RUNS = 12;
const MAX_RUN_BYTES = 48 * 1024 * 1024;
const MAX_TURNS = 100;
const MAX_TURN_BYTES = 512 * 1024;

const RUNS_KEY = "workbench.runs";
const FORM_KEY = "workbench.image-form";
const PLAYGROUND_KEY = "workbench.playground";

function imageSource(image: WorkbenchImage): string {
	return (image.data_url || image.url || "").trim();
}

export function runBytes(run: WorkbenchRun): number {
	return run.images.reduce((total, image) => total + imageSource(image).length, 0);
}

/** Newest-first, at most MAX_RUNS entries inside the byte budget. */
export function trimRuns(runs: WorkbenchRun[]): WorkbenchRun[] {
	const kept: WorkbenchRun[] = [];
	let bytes = 0;
	for (const run of runs) {
		if (kept.length >= MAX_RUNS) break;
		const size = runBytes(run);
		if (kept.length > 0 && bytes + size > MAX_RUN_BYTES) break;
		kept.push(run);
		bytes += size;
	}
	return kept;
}

function isRun(value: unknown): value is WorkbenchRun {
	const run = value as WorkbenchRun | null;
	return (
		!!run &&
		typeof run === "object" &&
		typeof run.id === "string" &&
		typeof run.model === "string" &&
		Array.isArray(run.images)
	);
}

export async function loadRuns(): Promise<WorkbenchRun[]> {
	const stored = await readConsoleState<WorkbenchRun[]>(RUNS_KEY).catch(() => null);
	return Array.isArray(stored) ? stored.filter(isRun) : [];
}

/**
 * Persists the history and reports what actually survived: a full store throws
 * a quota error, and the oldest entries are dropped until the write goes
 * through. An image too large for the fallback store leaves an empty history
 * rather than a half-written one — the session copy still has it.
 */
export async function saveRuns(runs: WorkbenchRun[]): Promise<WorkbenchRun[]> {
	let kept = trimRuns(runs);
	for (;;) {
		try {
			await writeConsoleState(RUNS_KEY, kept);
			return kept;
		} catch {
			if (kept.length <= 1) {
				if (!kept.length) return [];
				kept = [];
				continue;
			}
			kept = kept.slice(0, kept.length - 1);
		}
	}
}

export async function loadImageForm(): Promise<ImageFormState | null> {
	const stored = await readConsoleState<ImageFormState>(FORM_KEY).catch(() => null);
	return stored && typeof stored === "object" ? stored : null;
}

export async function saveImageForm(form: ImageFormState): Promise<void> {
	await writeConsoleState(FORM_KEY, form).catch(() => undefined);
}

export async function loadPlayground(): Promise<PlaygroundSession | null> {
	const stored = await readConsoleState<PlaygroundSession>(PLAYGROUND_KEY).catch(() => null);
	if (!stored || typeof stored !== "object" || !Array.isArray(stored.turns)) return null;
	return { ...stored, turns: stored.turns.filter(isTurn).map(normalizeTurn) };
}

export async function savePlayground(session: PlaygroundSession): Promise<void> {
	const turns = trimTurns(session.turns.map(normalizeTurn));
	await writeConsoleState(PLAYGROUND_KEY, { ...session, turns }).catch(() => undefined);
}

function isTurn(value: unknown): value is StoredTurn {
	const turn = value as StoredTurn | null;
	return !!turn && typeof turn === "object" && typeof turn.content === "string" && !!turn.role;
}

/**
 * A reload cannot resume a stream, so a turn stored mid-flight becomes what it
 * actually is: whatever arrived, marked complete.
 */
function normalizeTurn(turn: StoredTurn): StoredTurn {
	const { key, role, content, reasoning, status, at, error, stopped, latencyMs, upstreamStatus, channelName } = turn;
	return {
		key, role, content, reasoning: reasoning ?? "",
		status: status === "error" ? "error" : "complete",
		at, error, stopped, latencyMs, upstreamStatus, channelName,
	};
}

function trimTurns(turns: StoredTurn[]): StoredTurn[] {
	const kept = turns.slice(-MAX_TURNS);
	let bytes = kept.reduce((total, turn) => total + turn.content.length + turn.reasoning.length, 0);
	while (kept.length > 1 && bytes > MAX_TURN_BYTES) {
		const dropped = kept.shift()!;
		bytes -= dropped.content.length + dropped.reasoning.length;
	}
	return kept;
}
