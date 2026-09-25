import { ChevronDown, Search, X } from "lucide-react";
import { useMemo, useState } from "react";
import { useI18n } from "../i18n";
import { MODEL_GROUP_ORDER, autoModelGroup } from "../features/models/modelGroups";

/** One channel that can serve a candidate model name. */
export type ModelPickerChannel = { id: number; name: string };

/**
 * A candidate a token's allow/deny list can name.
 *
 * `name` is the string the filter compares against — the model id a client
 * sends, i.e. a route name (an alias is a route name too). The extra fields
 * exist so the row can explain where that name comes from: which channels can
 * reach it, which upstream models sit behind an alias, and whether the route
 * is currently parked.
 */
export type ModelOption = {
	name: string;
	channels?: ModelPickerChannel[];
	/** Vendor group label; falls back to autoModelGroup(name). */
	group?: string;
	/** Route is disabled — the name resolves to nothing right now. */
	disabled?: boolean;
	/** Wildcard route that expanded into this concrete name. */
	pattern?: string;
	/** Upstream model names behind an alias route ({real} mapping). */
	aliasOf?: string[];
};

type MergedOption = Required<Pick<ModelOption, "name">> & {
	channels: ModelPickerChannel[];
	group: string;
	disabled: boolean;
	pattern: string;
	aliasOf: string[];
};

/** Collapse duplicate names, unioning their channels and origins. */
function mergeOptions(options: ModelOption[]): MergedOption[] {
	const byName = new Map<string, MergedOption>();
	for (const option of options) {
		const name = option.name.trim();
		if (!name) continue;
		const existing = byName.get(name);
		if (!existing) {
			byName.set(name, {
				name,
				channels: [...(option.channels ?? [])],
				group: option.group?.trim() ?? "",
				disabled: Boolean(option.disabled),
				pattern: option.pattern ?? "",
				aliasOf: [...(option.aliasOf ?? [])],
			});
			continue;
		}
		for (const channel of option.channels ?? []) {
			if (!existing.channels.some((entry) => entry.id === channel.id)) {
				existing.channels.push(channel);
			}
		}
		for (const real of option.aliasOf ?? []) {
			if (!existing.aliasOf.includes(real)) existing.aliasOf.push(real);
		}
		// Only unroutable when every origin said so: one live route is enough.
		existing.disabled = existing.disabled && Boolean(option.disabled);
		if (!existing.group && option.group?.trim()) existing.group = option.group.trim();
		if (!existing.pattern && option.pattern) existing.pattern = option.pattern;
	}
	return [...byName.values()];
}

function matchesNeedle(option: MergedOption, needle: string): boolean {
	const haystack = [
		option.name,
		option.group,
		option.pattern,
		...option.aliasOf,
		...option.channels.map((channel) => channel.name),
	];
	return haystack.some((value) => value.toLowerCase().includes(needle));
}

/**
 * Searchable multi-select model chooser for the per-token allow/deny list.
 *
 * The list it shows is the set of names a client can actually send — routes,
 * not every model an upstream advertises — so a renamed (aliased) model is
 * findable under the name it answers to, and each row reports the channels
 * that can reach it. Selected names with no route left are surfaced instead
 * of silently blocking every request (renaming a model used to do exactly
 * that).
 */
export function ModelPicker({
	options,
	selected,
	onChange,
	placeholder,
	emptyLabel,
	className,
	grouped = true,
	flagMissing = false,
}: {
	options: ModelOption[];
	selected: string[];
	onChange: (next: string[]) => void;
	placeholder?: string;
	emptyLabel?: string;
	className?: string;
	/** Group candidates by vendor, collapsible. */
	grouped?: boolean;
	/** Warn about selected names no candidate carries (route renamed/removed). */
	flagMissing?: boolean;
}) {
	const { t } = useI18n();
	const [query, setQuery] = useState("");
	const [channelFilter, setChannelFilter] = useState<number | "">("");
	// Long catalogues start folded so groups stay scannable; short ones open
	// because there is nothing to collapse away.
	const [collapsed, setCollapsed] = useState<Set<string>>(
		() => new Set(options.length > 30 ? MODEL_GROUP_ORDER : []),
	);

	const merged = useMemo(() => mergeOptions(options), [options]);
	const selectedSet = useMemo(() => new Set(selected), [selected]);

	const channelChoices = useMemo(() => {
		const byId = new Map<number, { id: number; name: string; count: number }>();
		for (const option of merged) {
			for (const channel of option.channels) {
				const entry = byId.get(channel.id);
				if (entry) entry.count += 1;
				else byId.set(channel.id, { ...channel, count: 1 });
			}
		}
		return [...byId.values()].sort((a, b) => a.name.localeCompare(b.name));
	}, [merged]);

	const needle = query.trim().toLowerCase();
	const filtered = useMemo(() => {
		return merged.filter((option) => {
			if (
				channelFilter !== "" &&
				!option.channels.some((channel) => channel.id === channelFilter)
			) {
				return false;
			}
			if (!needle) return true;
			return matchesNeedle(option, needle);
		});
	}, [merged, channelFilter, needle]);

	const groups = useMemo(() => {
		if (!grouped) return [{ group: "", items: filtered }];
		const buckets = new Map<string, MergedOption[]>();
		for (const option of filtered) {
			const group = option.group || autoModelGroup(option.name);
			const bucket = buckets.get(group);
			if (bucket) bucket.push(option);
			else buckets.set(group, [option]);
		}
		const known = MODEL_GROUP_ORDER.filter((group) => buckets.has(group)).map(
			(group) => ({ group, items: buckets.get(group)! }),
		);
		const extra = [...buckets.keys()]
			.filter((group) => !MODEL_GROUP_ORDER.includes(group))
			.map((group) => ({ group, items: buckets.get(group)! }));
		return [...known, ...extra].sort((a, b) => b.items.length - a.items.length);
	}, [filtered, grouped]);

	// A filter that shortens the list opens every group: a collapsed header
	// must never be the reason a match looks missing.
	const forcedOpen = needle !== "" || channelFilter !== "";
	const toggleGroup = (group: string) => {
		setCollapsed((previous) => {
			const next = new Set(previous);
			if (next.has(group)) next.delete(group);
			else next.add(group);
			return next;
		});
	};

	const toggle = (name: string) => {
		if (selectedSet.has(name)) {
			onChange(selected.filter((entry) => entry !== name));
		} else {
			onChange([...selected, name]);
		}
	};

	const missing = flagMissing
		? selected.filter((name) => !merged.some((option) => option.name === name))
		: [];

	const selectFiltered = () => {
		const additions = filtered
			.map((option) => option.name)
			.filter((name) => !selectedSet.has(name));
		if (additions.length === 0) return;
		onChange([...selected, ...additions]);
	};

	return (
		<div className={`model-picker${className ? ` ${className}` : ""}`}>
			{selected.length > 0 ? (
				<>
					<div className="model-picker-head">
						<span className="model-picker-selected-count">
							{t("modelPicker.selectedCount", { n: selected.length })}
						</span>
						<button
							type="button"
							className="model-picker-clear"
							onClick={() => onChange([])}
						>
							{t("modelPicker.clearSelection")}
						</button>
					</div>
					<div className="model-picker-selected">
						{selected.map((name) => (
							<span
								key={name}
								className={`capability-chip is-key${missing.includes(name) ? " is-missing" : ""}`}
							>
								<span className="mono truncate" title={name}>
									{name}
								</span>
								<button
									type="button"
									className="model-picker-remove"
									aria-label={t("modelPicker.remove", { name })}
									onClick={() => toggle(name)}
								>
									<X size={11} />
								</button>
							</span>
						))}
					</div>
				</>
			) : null}

			{missing.length > 0 ? (
				<div className="model-picker-missing" role="status">
					<span className="model-picker-missing-copy">
						{t("modelPicker.missingTitle", { n: missing.length })}
					</span>
					<button
						type="button"
						className="model-picker-missing-clear"
						onClick={() =>
							onChange(selected.filter((name) => !missing.includes(name)))
						}
					>
						{t("modelPicker.missingClear", { n: missing.length })}
					</button>
				</div>
			) : null}

			<div className="model-picker-toolbar">
				<div className="model-picker-search">
					<Search size={14} />
					<input
						value={query}
						onChange={(event) => setQuery(event.target.value)}
						placeholder={placeholder ?? t("modelPicker.search")}
						spellCheck={false}
					/>
				</div>
				{channelChoices.length > 1 ? (
					<select
						className="model-picker-channel-filter"
						aria-label={t("modelPicker.channelFilter")}
						value={channelFilter}
						onChange={(event) =>
							setChannelFilter(
								event.target.value === "" ? "" : Number(event.target.value),
							)
						}
					>
						<option value="">{t("modelPicker.allChannels")}</option>
						{channelChoices.map((channel) => (
							<option key={channel.id} value={channel.id}>
								{`${channel.name} (${channel.count})`}
							</option>
						))}
					</select>
				) : null}
				{filtered.length > 0 ? (
					<button
						type="button"
						className="model-picker-selectall"
						onClick={selectFiltered}
					>
						{t("modelPicker.selectFiltered", { n: filtered.length })}
					</button>
				) : null}
			</div>

			<div className="model-picker-list">
				{filtered.length === 0 ? (
					<p className="model-picker-empty">{emptyLabel ?? t("modelPicker.empty")}</p>
				) : (
					groups.map(({ group, items }) => {
						const isCollapsed = !forcedOpen && collapsed.has(group);
						return (
							<div key={group || "__all"} className="model-picker-group">
								{group ? (
									<button
										type="button"
										className={`model-picker-group-head${isCollapsed ? " is-collapsed" : ""}`}
										aria-expanded={!isCollapsed}
										onClick={() => toggleGroup(group)}
									>
										<ChevronDown size={13} />
										<span className="model-picker-group-name">{group}</span>
										<span className="model-picker-group-count">{items.length}</span>
									</button>
								) : null}
								{!isCollapsed &&
									items.map((option) => {
										const shown = option.channels.slice(0, 2);
										const hidden = option.channels.length - shown.length;
										return (
											<label
												key={option.name}
												className={`model-picker-item${option.disabled ? " is-unroutable" : ""}`}
											>
												<input
													type="checkbox"
													checked={selectedSet.has(option.name)}
													onChange={() => toggle(option.name)}
												/>
												<span
													className="model-picker-name mono truncate"
													title={option.name}
												>
													{option.name}
												</span>
												{option.aliasOf.length > 0 ? (
													<span
														className="model-picker-badge is-alias"
														title={t("modelPicker.aliasTitle", {
															models: option.aliasOf.join(" · "),
														})}
													>
														{t("modelPicker.aliasBadge")}
													</span>
												) : null}
												{option.pattern ? (
													<span
														className="model-picker-badge is-pattern"
														title={t("modelPicker.patternTitle", {
															pattern: option.pattern,
														})}
													>
														{option.pattern}
													</span>
												) : null}
												{option.disabled ? (
													<span className="model-picker-badge is-off">
														{t("modelPicker.disabledBadge")}
													</span>
												) : null}
												{option.channels.length > 0 ? (
													<span
														className="model-picker-channels"
														title={t("modelPicker.channelsTitle", {
															channels: option.channels
																.map((channel) => channel.name)
																.join(" · "),
														})}
													>
														{shown.map((channel) => (
															<span
																key={channel.id}
																className="model-picker-channel"
															>
																{channel.name}
															</span>
														))}
														{hidden > 0 ? (
															<span className="model-picker-channel is-more">
																{`+${hidden}`}
															</span>
														) : null}
													</span>
												) : null}
											</label>
										);
									})}
							</div>
						);
					})
				)}
			</div>
		</div>
	);
}
