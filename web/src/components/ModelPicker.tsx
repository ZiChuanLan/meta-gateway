import { Search, X } from "lucide-react";
import { useMemo, useState } from "react";

/**
 * Searchable multi-select model chooser used for per-token model
 * allowlist / denylist restrictions. Selected models render as removable
 * chips; the search box filters the candidate list below it.
 */
export function ModelPicker({
	allModels,
	selected,
	onChange,
	placeholder,
	emptyLabel,
	className,
}: {
	allModels: string[];
	selected: string[];
	onChange: (next: string[]) => void;
	placeholder?: string;
	emptyLabel?: string;
	className?: string;
}) {
	const [query, setQuery] = useState("");
	const selectedSet = useMemo(() => new Set(selected), [selected]);

	const candidates = useMemo(() => {
		const unique = [...new Set(allModels.map((model) => model.trim()).filter(Boolean))].sort(
			(a, b) => a.localeCompare(b),
		);
		const needle = query.trim().toLowerCase();
		if (!needle) return unique;
		return unique.filter((model) => model.toLowerCase().includes(needle));
	}, [allModels, query]);

	const toggle = (model: string) => {
		if (selectedSet.has(model)) {
			onChange(selected.filter((entry) => entry !== model));
		} else {
			onChange([...selected, model]);
		}
	};

	return (
		<div className={`model-picker${className ? ` ${className}` : ""}`}>
			{selected.length > 0 ? (
				<div className="model-picker-selected">
					{selected.map((model) => (
						<span key={model} className="capability-chip is-key">
							<span className="mono truncate">{model}</span>
							<button
								type="button"
								className="model-picker-remove"
								aria-label={`Remove ${model}`}
								onClick={() => toggle(model)}
							>
								<X size={11} />
							</button>
						</span>
					))}
				</div>
			) : null}
			<div className="model-picker-search">
				<Search size={14} />
				<input
					value={query}
					onChange={(event) => setQuery(event.target.value)}
					placeholder={placeholder}
					spellCheck={false}
				/>
			</div>
			<div className="model-picker-list">
				{candidates.length === 0 ? (
					<p className="model-picker-empty">{emptyLabel}</p>
				) : (
					candidates.map((model) => (
						<label key={model} className="model-picker-item">
							<input
								type="checkbox"
								checked={selectedSet.has(model)}
								onChange={() => toggle(model)}
							/>
							<span className="mono truncate" title={model}>
								{model}
							</span>
						</label>
					))
				)}
			</div>
		</div>
	);
}
