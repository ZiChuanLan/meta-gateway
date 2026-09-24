import { useEffect, useMemo, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ChevronDown, ChevronRight } from "lucide-react";
import { api } from "../../api/client";
import type { PluginConfigField } from "../../api/types";
import { ErrorState, Loading } from "../../components/ui";
import { Button } from "../../components/ui";
import { useAdminMutation } from "../../hooks/useAdminMutation";
import { useI18n } from "../../i18n";
import { ModelGroupsField } from "./ModelGroupsField";

type FieldValue = string | number | boolean;

/** The subset of the admin client this form needs. */
type ConfigService = Pick<
	ReturnType<typeof api>,
	"pluginConfig" | "savePluginConfig"
>;

/**
 * Renders a plugin's declared config_fields and saves them.
 *
 * One form, two hosts: the Store opens it in a dialog, and the plugin's own page
 * shows it inline. The plugin page is where an operator already is when they
 * wonder "what is this thing configured to do", so the settings have to be
 * readable there — bouncing to another page to change one field is how a plugin
 * keeps its defaults forever.
 */
export function PluginConfigForm({
	pluginId,
	service,
	onSaved,
	models = [],
}: {
	pluginId: string;
	service: ConfigService;
	/** Called after a successful save (the dialog closes; the page keeps it open). */
	onSaved?: () => void;
	/** Model names offered by the gateway, for fields that pick models. */
	models?: string[];
}) {
	const { t } = useI18n();
	const [values, setValues] = useState<Record<string, FieldValue> | null>(null);
	const [raw, setRaw] = useState("");
	const [error, setError] = useState("");
	const [saved, setSaved] = useState(false);
	// Advanced fields are collapsed by default: they matter to someone specific
	// and are noise to everyone else, and the manifest is what decides which is
	// which.
	const [showAdvanced, setShowAdvanced] = useState(false);
	const info = useQuery({
		queryKey: ["plugin-config", pluginId],
		queryFn: ({ signal }) => service.pluginConfig(pluginId, signal),
	});
	const fields = useMemo(() => info.data?.fields ?? [], [info.data]);

	useEffect(() => {
		if (!info.data || values !== null) return;
		let stored: Record<string, unknown> = {};
		try {
			stored = JSON.parse(info.data.config || "{}") as Record<string, unknown>;
		} catch {
			stored = {};
		}
		const parsed: Record<string, FieldValue> = {};
		for (const field of fields) {
			const value = stored[field.key];
			if (field.type === "number") {
				parsed[field.key] =
					typeof value === "number" ? value : ((field.default as number) ?? 0);
			} else if (field.type === "bool") {
				parsed[field.key] =
					typeof value === "boolean" ? value : Boolean(field.default ?? false);
			} else if (field.type === "select") {
				parsed[field.key] =
					typeof value === "string"
						? value
						: ((field.default as string) ?? field.options?.[0] ?? "");
			} else if (field.type === "model_groups") {
				// Structured: the value is JSON in both directions. The declared
				// default may already BE a JSON string (manifests write one) or a real
				// array, so it is taken as-is when it is a string — encoding it again
				// would hand the editor a quoted blob it cannot parse.
				const fallback =
					typeof field.default === "string"
						? field.default
						: JSON.stringify(field.default ?? []);
				parsed[field.key] =
					typeof value === "string"
						? value
						: value == null
							? fallback
							: JSON.stringify(value);
			} else {
				parsed[field.key] =
					typeof value === "string" ? value : ((field.default as string) ?? "");
			}
		}
		setValues(parsed);
		if (fields.length === 0) setRaw(info.data.config || "{}");
	}, [info.data, values, fields]);

	const save = useAdminMutation({
		mutationFn: () => {
			if (fields.length > 0) {
				return service.savePluginConfig(pluginId, JSON.stringify(values ?? {}));
			}
			let parsed: unknown;
			try {
				parsed = JSON.parse(raw);
			} catch {
				throw new Error(t("plugins.configInvalidJson"));
			}
			if (!parsed || typeof parsed !== "object" || Array.isArray(parsed)) {
				throw new Error(t("plugins.configInvalidJson"));
			}
			return service.savePluginConfig(pluginId, raw);
		},
		invalidateKeys: [["plugin-config", pluginId]],
		toastOnError: false,
		onSuccess: () => {
			setSaved(true);
			onSaved?.();
		},
		onError: (err: unknown) => {
			setError(err instanceof Error ? err.message : String(err));
		},
	});

	const set = (key: string, value: FieldValue) => {
		setSaved(false);
		setValues((current) => ({ ...(current ?? {}), [key]: value }));
	};

	const inputFor = (field: PluginConfigField) => {
		const value = values?.[field.key];
		switch (field.type) {
			case "model_groups":
				return (
					<ModelGroupsField
						value={String(value ?? "")}
						onChange={(next) => set(field.key, next)}
						models={models}
					/>
				);
			case "text":
				return (
					<textarea
						rows={4}
						className="mono"
						value={String(value ?? "")}
						onChange={(event) => set(field.key, event.target.value)}
					/>
				);
			case "number":
				return (
					<input
						type="number"
						value={String(value ?? 0)}
						onChange={(event) =>
							set(field.key, event.target.value === "" ? 0 : Number(event.target.value))
						}
					/>
				);
			case "bool":
				return (
					<label className="check">
						<input
							type="checkbox"
							checked={Boolean(value)}
							onChange={(event) => set(field.key, event.target.checked)}
						/>
						<span>{t("plugins.configEnabled")}</span>
					</label>
				);
			case "model":
				// A model name is a choice, not prose: the options are the models this
				// gateway can actually route, so the field cannot be set to something
				// that has no route.
				return (
					<select
						value={String(value ?? "")}
						onChange={(event) => set(field.key, event.target.value)}
					>
						<option value="">{t("plugins.modelNone")}</option>
						{models.map((model) => (
							<option key={model} value={model}>
								{model}
							</option>
						))}
					</select>
				);
			case "select":
				return (
					<select
						value={String(value ?? "")}
						onChange={(event) => set(field.key, event.target.value)}
					>
						{(field.options ?? []).map((option) => (
							<option key={option} value={option}>
								{option}
							</option>
						))}
					</select>
				);
			default:
				return (
					<input
						type={field.type === "secret" ? "password" : "text"}
						className="mono"
						value={String(value ?? "")}
						onChange={(event) => set(field.key, event.target.value)}
					/>
				);
		}
	};

	if (info.isPending) return <Loading />;
	if (info.isError) return <ErrorState error={info.error} />;

	const renderField = (field: PluginConfigField) => (
		<label
			className={field.type === "model_groups" ? "field is-wide" : "field"}
			key={field.key}
		>
			<span>
				{field.label || field.key}
				{field.required ? " *" : ""}
			</span>
			{inputFor(field)}
			{field.description ? <small className="muted">{field.description}</small> : null}
			{field.type === "secret" ? (
				<small className="muted">{t("plugins.configSecretHint")}</small>
			) : null}
		</label>
	);

	if (fields.length === 0) {
		return (
			<div className="plugin-config-form">
				<label className="field">
					<span>{t("plugins.configJsonLabel")}</span>
					<textarea
						className="mono"
						rows={8}
						value={raw}
						onChange={(event) => {
							setSaved(false);
							setRaw(event.target.value);
						}}
					/>
				</label>
				{error ? <p className="is-danger">{error}</p> : null}
				<div className="plugin-config-actions">
					<Button disabled={save.isPending} onClick={() => save.mutate(undefined)}>
						{save.isPending ? t("common.working") : t("plugins.saveBtn")}
					</Button>
					{saved && !save.isPending ? (
						<span className="plugin-config-saved">{t("plugins.configSaved")}</span>
					) : null}
				</div>
			</div>
		);
	}

	const basic = fields.filter((field) => !field.advanced);
	const advanced = fields.filter((field) => field.advanced);

	return (
		<div className="plugin-config-form">
			<div className="form-stack">{basic.map(renderField)}</div>
			{advanced.length > 0 ? (
				<div className="config-advanced">
					<button
						type="button"
						className="config-advanced-toggle"
						onClick={() => setShowAdvanced((open) => !open)}
						aria-expanded={showAdvanced}
					>
						{showAdvanced ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
						{t("plugins.advancedFields", { count: advanced.length })}
					</button>
					{showAdvanced ? <div className="form-stack">{advanced.map(renderField)}</div> : null}
				</div>
			) : null}
			{error ? <p className="is-danger">{error}</p> : null}
			<div className="plugin-config-actions">
				<Button disabled={save.isPending} onClick={() => save.mutate(undefined)}>
					{save.isPending ? t("common.working") : t("plugins.saveBtn")}
				</Button>
				{saved && !save.isPending ? (
					<span className="plugin-config-saved">{t("plugins.configSaved")}</span>
				) : null}
			</div>
		</div>
	);
}
