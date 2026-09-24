import { Plus, Sparkles, Trash2, X } from "lucide-react";
import { useI18n } from "../../i18n";
import { Button } from "../../components/ui";

/** One named shortlist: a scenario, and the models that may answer inside it. */
export interface ModelGroup {
	name: string;
	hint: string;
	models: string[];
}

/**
 * Well-known scenario presets. A preset is a skeleton only: name and hint.
 * Models are deliberately NOT pre-filled — the manifest and the console both
 * guess worse than the operator about which model answers what, and an empty
 * shortlist is one tap on "add model" away from correct.
 */
const SCENARIO_PRESETS: Array<{
	name: string;
	hint: string;
}> = [
	{ name: "code-simple", hint: "简单代码：小改动、脚本、一次性工具" },
	{ name: "code-complex", hint: "复杂代码：有深度的实现与重构" },
	{ name: "thinking", hint: "复杂推理、架构思考" },
	{ name: "bugfix", hint: "bug 修复：定位缺陷、最小修复" },
	{ name: "chat", hint: "日常回答" },
];

/**
 * Builds preset scenarios as empty skeletons, in preset order.
 *
 * The presets exist to name the four work kinds an operator would otherwise
 * have to invent; which models belong in each is their call. No intersection
 * with the live model list happens here — an empty shortlist costs nothing
 * (the plugin drops it at runtime) and pre-filling would just be guesses the
 * operator has to un-pick.
 */
export function suggestModelGroups(): ModelGroup[] {
	return SCENARIO_PRESETS.map((preset) => ({
		name: preset.name,
		hint: preset.hint,
		models: [],
	}));
}

/**
 * Reads a stored model_groups value.
 *
 * Tolerant on purpose: the field may hold a value an operator hand-wrote, an
 * older format, or plain garbage. Returning an empty list keeps all of that out
 * of the render path, where a throw would take the whole settings panel down
 * over one bad field.
 *
 * An entry with no name is KEPT. The value is a controlled prop, so whatever
 * this function drops disappears from the editor on the next render — and an
 * empty row is exactly what "add scenario" just created. Dropping it made the
 * button look broken. Unusable entries are discarded where they matter instead:
 * the plugin ignores any scenario without a name or without models.
 */
export function parseModelGroups(raw: string): ModelGroup[] {
	const trimmed = raw.trim();
	if (!trimmed.startsWith("[")) return [];
	let decoded: unknown;
	try {
		decoded = JSON.parse(trimmed);
	} catch {
		return [];
	}
	if (!Array.isArray(decoded)) return [];
	const out: ModelGroup[] = [];
	for (const item of decoded) {
		if (!item || typeof item !== "object" || Array.isArray(item)) continue;
		const entry = item as { name?: unknown; hint?: unknown; models?: unknown };
		const models = Array.isArray(entry.models)
			? entry.models
					.filter((model): model is string => typeof model === "string")
					.map((model) => model.trim())
					.filter(Boolean)
			: [];
		out.push({
			name: typeof entry.name === "string" ? entry.name.trim() : "",
			hint: typeof entry.hint === "string" ? entry.hint.trim() : "",
			models,
		});
	}
	return out;
}

/**
 * Serializes back to the stored shape.
 *
 * Empty scenarios are KEPT. The editor has to be able to hold a half-finished
 * row — a name typed before the models are picked — or clicking "add scenario"
 * would silently do nothing, because the row it just created leaves again on
 * the next render. A blank row costs nothing at runtime: the plugin drops
 * entries without a name or without models when it reads them.
 */
export function formatModelGroups(groups: ModelGroup[]): string {
	return JSON.stringify(
		groups.map((group) => ({
			name: group.name.trim(),
			hint: group.hint.trim(),
			models: group.models,
		})),
	);
}

/**
 * An editor for a model_groups field.
 *
 * The stored value is structured, so the editor is too: a textarea would make
 * the operator responsible for a syntax that nothing validates until the plugin
 * fails to parse it. Models come from the gateway's own list, so a scenario
 * cannot be built around a model that has no route.
 */
export function ModelGroupsField({
	value,
	onChange,
	models,
}: {
	value: string;
	onChange: (next: string) => void;
	models: string[];
}) {
	const { t } = useI18n();
  const groups = parseModelGroups(value);

  const update = (next: ModelGroup[]) => onChange(formatModelGroups(next));
  const replace = (index: number, patch: Partial<ModelGroup>) =>
    update(groups.map((group, i) => (i === index ? { ...group, ...patch } : group)));

  const addGroup = () =>
    update([...groups, { name: "", hint: "", models: [] }]);

  // One click from the empty state to a usable starting point: four named
  // work kinds, no models pre-picked — which model answers what is the
  // operator's call, and an empty shortlist is one tap on "add model" away
  // from correct.
  const applyPresets = () => update(suggestModelGroups());

  return (
    <div className="model-groups">
      {groups.length === 0 ? (
        <p className="model-groups-empty">{t("plugins.modelGroups.empty")}</p>
      ) : null}
      {groups.length === 0 ? (
        <Button
          variant="secondary"
          icon={<Sparkles size={14} />}
          onClick={applyPresets}
        >
          {t("plugins.modelGroups.usePresets")}
        </Button>
      ) : null}
			{groups.map((group, index) => {
				const remaining = models.filter((model) => !group.models.includes(model));
				return (
					<div className="model-group" key={`group-${index}`}>
						<div className="model-group-head">
							<input
								className="model-group-name"
								placeholder={t("plugins.modelGroups.namePlaceholder")}
								value={group.name}
								onChange={(event) => replace(index, { name: event.target.value })}
							/>
							<input
								className="model-group-hint"
								placeholder={t("plugins.modelGroups.hintPlaceholder")}
								value={group.hint}
								onChange={(event) => replace(index, { hint: event.target.value })}
							/>
							<button
								type="button"
								className="model-group-remove"
								aria-label={t("plugins.modelGroups.remove")}
								onClick={() => update(groups.filter((_, i) => i !== index))}
							>
								<Trash2 size={14} />
							</button>
						</div>
						<div className="model-group-models">
							{group.models.map((model) => (
								<span className="model-chip" key={model}>
									<span className="mono">{model}</span>
									<button
										type="button"
										aria-label={t("plugins.modelGroups.removeModel", { model })}
										onClick={() =>
											replace(index, {
												models: group.models.filter((entry) => entry !== model),
											})
										}
									>
										<X size={12} />
									</button>
								</span>
							))}
							<select
								className="model-group-add"
								aria-label={t("plugins.modelGroups.pickModel")}
								value=""
								disabled={remaining.length === 0}
								onChange={(event) => {
									const model = event.target.value;
									if (!model) return;
									replace(index, { models: [...group.models, model] });
								}}
							>
								<option value="">
									{remaining.length === 0
										? t("plugins.modelGroups.allPicked")
										: t("plugins.modelGroups.addModel")}
								</option>
								{remaining.map((model) => (
									<option key={model} value={model}>
										{model}
									</option>
								))}
							</select>
						</div>
					</div>
				);
			})}
			<Button variant="secondary" icon={<Plus size={14} />} onClick={addGroup}>
				{t("plugins.modelGroups.addScenario")}
			</Button>
			{models.length === 0 ? (
				<p className="muted" style={{ fontSize: 12, marginTop: 6 }}>
					{t("plugins.modelGroups.noModels")}
				</p>
			) : null}
		</div>
	);
}
