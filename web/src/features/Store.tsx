import {
	Cable,
	ChevronDown,
	Download,
	ExternalLink,
	Package,
	Pencil,
	Plug,
	Power,
	PowerOff,
	RefreshCw,
	Settings2,
	Trash2,
} from "lucide-react";
import { Link } from "react-router-dom";
import { useMemo, useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import type { ModuleStatus, PluginConfigField, PluginRecord } from "../api/types";
import { EntityState } from "../components/EntityState";
import { useAdminMutation } from "../hooks/useAdminMutation";
import {
	ADDON_CHECKIN,
	ADDON_EXCHANGE,
	MODULES_QUERY_KEY,
	useModules,
} from "../hooks/useModules";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { Button, Dialog, ErrorState, Loading, Page, StatusBadge } from "../components/ui";

// Only invalidate always-safe keys here. Gated add-on queries (checkin-logs,
// webdav-*) are owned by their panels with enabled: moduleOn — invalidating them
// while the add-on is off causes noisy 404s after Store toggles.
const STORE_INVALIDATE = [
	MODULES_QUERY_KEY,
	["plugins"],
	["plugins-catalog"],
	["plugins-market"],
	["channel-overviews"],
	["credentials"],
];

interface MarketPlugin {
	id: string;
	name: string;
	description?: string;
	author?: string;
	version?: string;
	logo?: string;
	homepage?: string;
	license?: string;
	tags?: string[];
	url: string;
	install?: { type?: string };
	source: { id: string; name: string; url: string };
}

interface SidecarConfig {
	url: string;
	api_key?: string;
	page_path?: string;
	health_path?: string;
	api_prefix?: string;
	channel_path?: string;
}

function sidecarOf(rec: PluginRecord | undefined): SidecarConfig | null {
	if (!rec?.meta_json) return null;
	try {
		const meta = JSON.parse(rec.meta_json) as { sidecar?: SidecarConfig };
		return meta.sidecar ?? null;
	} catch {
		return null;
	}
}

export function Store() {
	const { t } = useI18n();
	const { client } = useSession();
	const service = api(client!);
	const modules = useModules();
	const [addUrl, setAddUrl] = useState("");
	const [addKey, setAddKey] = useState("");
	const [addId, setAddId] = useState("");
	const [addName, setAddName] = useState("");
	const [addPage, setAddPage] = useState("");
	const [addHealth, setAddHealth] = useState("");
	const [addPrefix, setAddPrefix] = useState("");
	const [advancedOpen, setAdvancedOpen] = useState(false);
	const [addError, setAddError] = useState("");
	const [editing, setEditing] = useState<ModuleStatus | null>(null);

	const activate = useAdminMutation({
		mutationFn: (id: string) => service.activatePlugin(id),
		invalidateKeys: STORE_INVALIDATE,
		pendingIdOf: (id) => id,
	});
	const disable = useAdminMutation({
		mutationFn: (id: string) => service.disablePlugin(id),
		invalidateKeys: STORE_INVALIDATE,
		pendingIdOf: (id) => id,
	});
	const uninstall = useAdminMutation({
		mutationFn: (id: string) => service.uninstallPlugin(id),
		invalidateKeys: STORE_INVALIDATE,
		pendingIdOf: (id) => id,
	});
	const add = useAdminMutation({
		mutationFn: () =>
				service.registerPlugin(addUrl.trim(), addKey.trim(), {
					id: addId.trim(),
					name: addName.trim(),
					pagePath: addPage.trim(),
					healthPath: addHealth.trim(),
					apiPrefix: addPrefix.trim(),
				}),
		invalidateKeys: STORE_INVALIDATE,
		toastOnError: false,
		onSuccess: () => {
			setAddUrl("");
			setAddKey("");
			setAddId("");
			setAddName("");
			setAddPage("");
			setAddHealth("");
			setAddPrefix("");
			setAdvancedOpen(false);
			setAddError("");
		},
		onError: (err: unknown) => {
			const msg = err instanceof Error ? err.message : "";
			// No /plugin.json at the service? Unfold the manual fields.
			if (msg.includes("plugin_manifest")) {
				setAdvancedOpen(true);
			}
			setAddError(msg || t("plugins.addFailed"));
		},
	});

	const busyId =
		activate.pendingId ?? disable.pendingId ?? uninstall.pendingId ?? null;

	const market = useQuery({
		queryKey: ["plugins-market"],
		queryFn: ({ signal }) => service.pluginsMarket(signal),
		staleTime: 60_000,
	});
	const installMarket = useAdminMutation({
		mutationFn: (args: { id: string; source?: string }) =>
			service.installMarketPlugin(args.id, { source: args.source }),
		invalidateKeys: STORE_INVALIDATE,
		pendingIdOf: (args) => args.id,
	});

	// Plugin records (with meta_json) for channel-path lookups and the edit
	// dialog back-fill.
	const { data: pluginRecords } = useQuery({
		queryKey: ["plugins"],
		queryFn: ({ signal }) => service.plugins(signal),
		staleTime: 30_000,
	});
	const sidecarOfId = (id: string) =>
		sidecarOf(pluginRecords?.find((rec) => rec.id === id));
	const [channelFor, setChannelFor] = useState<ModuleStatus | null>(null);
	const [configFor, setConfigFor] = useState<ModuleStatus | null>(null);

	const enabledAddons = useMemo(
		() => modules.addons.filter((item) => item.enabled && item.installed),
		[modules.addons],
	);
	const availableAddons = useMemo(
		() => modules.addons.filter((item) => !item.enabled || !item.installed),
		[modules.addons],
	);
	const orphans = useMemo(
		() =>
			modules.modules.filter(
				(item) =>
					item.kind !== "core" &&
					item.installed &&
					!item.can_toggle &&
					item.id !== ADDON_EXCHANGE &&
					item.id !== ADDON_CHECKIN,
			),
		[modules.modules],
	);

	return (
		<Page
			kicker={t("store.kicker")}
			title={t("store.title")}
			description={t("store.description")}
			actions={
				<Button
					variant="secondary"
					icon={<RefreshCw size={16} />}
					onClick={() => void modules.refetch()}
				>
					{t("common.refresh")}
				</Button>
			}
		>
			<div className="ops-canvas store-layout">
				<div className="store-col store-col-side">
					<section className="store-section">
						<header className="store-section-head">
							<h2>{t("plugins.add")}</h2>
							<p>{t("plugins.addHint")}</p>
						</header>
						<div className="plugin-add-row">
							<input
								className="mono"
								value={addUrl}
								onChange={(e) => setAddUrl(e.target.value)}
								placeholder="http://127.0.0.1:9100"
								spellCheck={false}
							/>
							<input
								type="password"
								value={addKey}
								onChange={(e) => setAddKey(e.target.value)}
								placeholder={t("plugins.keyPlaceholder")}
							/>
							<Button
								icon={<Plug size={15} />}
								disabled={add.isPending || !addUrl.trim()}
								onClick={() => add.mutate(undefined)}
							>
								{add.isPending ? t("common.working") : t("plugins.addBtn")}
							</Button>
						</div>
						<button
						type="button"
							aria-expanded={advancedOpen}
							className="plugin-add-toggle"
							onClick={() => setAdvancedOpen((v) => !v)}
						>
							<ChevronDown
								size={14}
								className={advancedOpen ? "chevron-flip is-open" : "chevron-flip"}
							/>
							{t("plugins.advanced")}
						</button>
						{advancedOpen ? (
							<div className="plugin-add-advanced">
								<p className="muted">{t("plugins.advancedHint")}</p>
								<div className="plugin-add-row">
									<input
										className="mono"
										value={addId}
										onChange={(e) => setAddId(e.target.value)}
										placeholder={t("plugins.idPlaceholder")}
										spellCheck={false}
									/>
									<input
										value={addName}
										onChange={(e) => setAddName(e.target.value)}
										placeholder={t("plugins.namePlaceholder")}
									/>
								</div>
								<div className="plugin-add-row">
									<input
										className="mono"
										value={addPage}
										onChange={(e) => setAddPage(e.target.value)}
										placeholder={t("plugins.pagePlaceholder")}
										spellCheck={false}
									/>
								<input
									className="mono"
									value={addHealth}
									onChange={(e) => setAddHealth(e.target.value)}
									placeholder={t("plugins.healthPlaceholder")}
									spellCheck={false}
								/>
							</div>
							<div className="plugin-add-row">
								<input
									className="mono"
									value={addPrefix}
									onChange={(e) => setAddPrefix(e.target.value)}
									placeholder={t("plugins.prefixPlaceholder")}
									spellCheck={false}
								/>
							</div>
							</div>
						) : null}
						{addError ? <div className="inline-error">{addError}</div> : null}
					</section>

						<section className="store-section">
							<header className="store-section-head">
								<h2>{t("store.section.available")}</h2>
								<p>{t("store.section.availableHint")}</p>
							</header>
							{availableAddons.length === 0 ? (
								<p className="detail-empty">{t("store.emptyAvailable")}</p>
							) : (
								<div className="module-grid">
									{availableAddons.map((row) => (
										<ModuleCard
											key={row.id}
											row={row}
											busy={busyId === row.id}
											onActivate={() => activate.mutate(row.id)}
											t={t}
										/>
									))}
								</div>
							)}
						</section>

						<section className="store-section">
							<header className="store-section-head">
								<h2>{t("store.section.market")}</h2>
								<p>{t("store.section.marketHint")}</p>
							</header>
							{market.isLoading ? (
								<p className="detail-empty">{t("common.loading")}</p>
							) : market.isError ? (
								<p className="detail-empty is-danger">
									{t("store.marketFailed")}
								</p>
							) : (market.data?.plugins ?? []).length === 0 ? (
								<p className="detail-empty">{t("store.marketEmpty")}</p>
							) : (
								<div className="market-grid">
									{(market.data?.plugins ?? []).map((p) => (
										<MarketCard
											key={`${p.source.id}:${p.id}`}
											plugin={p}
											installedVersion={enabledAddons.find((m) => m.id === p.id)?.version}
											busy={installMarket.pendingId === p.id}
											onInstall={() => installMarket.mutate({ id: p.id, source: p.source.id })}
											t={t}
										/>
									))}
								</div>
							)}
						</section>
					</div>

				<div className="store-col store-col-main">
					<EntityState
						isLoading={modules.isPending}
						isError={modules.isError}
						error={modules.error}
						isEmpty={false}
						retry={() => modules.refetch()}
					>
						<section className="store-section">
							<header className="store-section-head">
								<h2>{t("store.section.enabled")}</h2>
								<p>{t("store.section.enabledHint")}</p>
							</header>
							{enabledAddons.length === 0 ? (
								<p className="detail-empty">{t("store.emptyEnabled")}</p>
							) : (
								<div className="plugin-rows">
									{enabledAddons.map((row) => (
									<PluginRow
										key={row.id}
										row={row}
										busy={busyId === row.id}
										onEdit={
											row.source === "sidecar"
												? () => setEditing(row)
												: undefined
											}
										onChannel={
											row.source === "sidecar" &&
											sidecarOfId(row.id)?.channel_path
												? () => setChannelFor(row)
												: undefined
											}
										onDisable={() => disable.mutate(row.id)}
									onUninstall={
										row.source === "sidecar" || row.source?.startsWith("market:")
											? () => uninstall.mutate(row.id)
											: undefined
										}
										t={t}
									/>
									))}
								</div>
							)}
						</section>

						{orphans.length > 0 ? (
							<section className="store-section">
								<header className="store-section-head">
									<h2>{t("store.orphans")}</h2>
									<p>{t("store.orphanHint")}</p>
								</header>
								<div className="plugin-rows">
									{orphans.map((row) => (
										<PluginRow
											key={row.id}
											row={row}
											busy={busyId === row.id}
											onUninstall={() => uninstall.mutate(row.id)}
											t={t}
										/>
									))}
								</div>
							</section>
						) : null}
					</EntityState>
				</div>
			</div>

			{editing ? (
				<EditPluginDialog
					row={editing}
					service={service}
					onClose={() => setEditing(null)}
				/>
			) : null}
			{channelFor ? (
				<CreateChannelDialog
					row={channelFor}
					service={service}
					defaults={sidecarOfId(channelFor.id)}
					onClose={() => setChannelFor(null)}
				/>
			) : null}
			{configFor ? (
				<PluginConfigDialog
					row={configFor}
					service={service}
					onClose={() => setConfigFor(null)}
				/>
			) : null}
		</Page>
	);
}

function PluginRow({
	row,
	busy,
	onEdit,
	onChannel,
	onConfig,
	onDisable,
	onUninstall,
	t,
}: {
	row: ModuleStatus;
	busy: boolean;
	onEdit?: () => void;
	onChannel?: () => void;
	onConfig?: () => void;
	onDisable?: () => void;
	onUninstall?: () => void;
	t: (key: string, vars?: Record<string, string | number>) => string;
}) {
	const description = moduleDescription(row, t);
	const badge = !row.installed
		? "available"
		: row.enabled
			? "enabled"
			: "disabled";
	return (
		<div className="plugin-row">
			<div className="plugin-row-icon">
				<Package size={17} />
			</div>
			<div className="plugin-row-body">
				<div className="plugin-row-title">
					<strong>{row.name}</strong>
					<small className="mono">
						{row.id} · {row.version}
					</small>
				</div>
				<p>{description}</p>
			</div>
			<div className="plugin-row-actions">
				{row.open_path ? (
					<Link className="button button-secondary" to={row.open_path}>
						<ExternalLink size={14} />
						{t("store.openFeature")}
					</Link>
				) : null}
				{onEdit ? (
					<Button
						variant="secondary"
						icon={<Pencil size={14} />}
						disabled={busy}
						onClick={onEdit}
					>
						{t("plugins.edit")}
					</Button>
				) : null}
				{onChannel ? (
					<Button
						variant="secondary"
						icon={<Cable size={14} />}
						disabled={busy}
						onClick={onChannel}
					>
						{t("plugins.createChannel")}
					</Button>
				) : null}
				{onConfig ? (
					<Button
						variant="secondary"
						icon={<Settings2 size={14} />}
						disabled={busy}
						onClick={onConfig}
					>
						{t("plugins.config")}
					</Button>
				) : null}
				{onDisable ? (
					<Button
						variant="secondary"
						icon={<PowerOff size={14} />}
						disabled={busy}
						onClick={onDisable}
					>
						{t("store.deactivate")}
					</Button>
				) : null}
				{onUninstall ? (
					<Button
						variant="quiet"
						icon={<Trash2 size={14} />}
						disabled={busy}
						onClick={onUninstall}
					>
						{t("store.uninstall")}
					</Button>
				) : null}
			</div>
			<StatusBadge value={badge} />
		</div>
	);
}

function PluginConfigDialog({
	row,
	service,
	onClose,
}: {
	row: ModuleStatus;
	service: ReturnType<typeof api>;
	onClose: () => void;
}) {
	const { t } = useI18n();
	type FieldValue = string | number | boolean;
	const [values, setValues] = useState<Record<string, FieldValue> | null>(null);
	const [raw, setRaw] = useState("");
	const [error, setError] = useState("");
	const info = useQuery({
		queryKey: ["plugin-config", row.id],
		queryFn: ({ signal }) => service.pluginConfig(row.id, signal),
	});
	const fields = info.data?.fields ?? [];
	useEffect(() => {
		if (!info.data || values !== null) return;
		let stored: Record<string, unknown> = {};
		try {
			stored = JSON.parse(info.data.config || "{}") as Record<
				string,
				unknown
			>;
		} catch {
			stored = {};
		}
		const parsed: Record<string, FieldValue> = {};
		for (const f of fields) {
			const value = stored[f.key];
			if (f.type === "number") {
				parsed[f.key] =
					typeof value === "number" ? value : ((f.default as number) ?? 0);
			} else if (f.type === "bool") {
				parsed[f.key] =
					typeof value === "boolean" ? value : Boolean(f.default ?? false);
			} else if (f.type === "select") {
				parsed[f.key] =
					typeof value === "string"
						? value
						: ((f.default as string) ?? f.options?.[0] ?? "");
			} else {
				parsed[f.key] =
					typeof value === "string" ? value : ((f.default as string) ?? "");
			}
		}
		setValues(parsed);
		if (fields.length === 0) setRaw(info.data.config || "{}");
	}, [info.data, values, fields]);

	const save = useAdminMutation({
		mutationFn: () => {
			if (fields.length > 0) {
				return service.savePluginConfig(row.id, JSON.stringify(values ?? {}));
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
			return service.savePluginConfig(row.id, raw);
		},
		invalidateKeys: [...STORE_INVALIDATE, ["plugin-config", row.id]],
		toastOnError: false,
		onSuccess: onClose,
		onError: (err: unknown) => {
			setError(err instanceof Error ? err.message : String(err));
		},
	});

	const set = (key: string, value: FieldValue) =>
		setValues((current) => ({ ...(current ?? {}), [key]: value }));
	const inputFor = (f: PluginConfigField) => {
		const value = values?.[f.key];
		switch (f.type) {
			case "text":
				return (
					<textarea
						rows={4}
						className="mono"
						value={String(value ?? "")}
						onChange={(e) => set(f.key, e.target.value)}
					/>
				);
			case "number":
				return (
					<input
						type="number"
						value={String(value ?? 0)}
						onChange={(e) =>
							set(f.key, e.target.value === "" ? 0 : Number(e.target.value))
						}
					/>
				);
			case "bool":
				return (
					<label className="check">
						<input
							type="checkbox"
							checked={Boolean(value)}
							onChange={(e) => set(f.key, e.target.checked)}
						/>
						<span>{t("plugins.configEnabled")}</span>
					</label>
				);
			case "select":
				return (
					<select
						value={String(value ?? "")}
						onChange={(e) => set(f.key, e.target.value)}
					>
						{(f.options ?? []).map((option) => (
							<option key={option} value={option}>
								{option}
							</option>
						))}
					</select>
				);
			default:
				return (
					<input
						type={f.type === "secret" ? "password" : "text"}
						className="mono"
						value={String(value ?? "")}
						onChange={(e) => set(f.key, e.target.value)}
					/>
				);
		}
	};

	return (
		<Dialog
			title={`${t("plugins.configTitle")} — ${row.name}`}
			onClose={onClose}
			actions={
				<>
					<Button variant="secondary" onClick={onClose}>
						{t("common.cancel")}
					</Button>
					<Button
						disabled={save.isPending || !info.data}
						onClick={() => save.mutate(undefined)}
					>
						{save.isPending ? t("common.working") : t("plugins.saveBtn")}
					</Button>
				</>
			}
		>
			<p className="muted" style={{ fontSize: 12, marginBottom: 10 }}>
				{t("plugins.configHint")}
			</p>
			{info.isPending ? (
				<Loading />
			) : info.isError ? (
				<ErrorState error={info.error} />
			) : fields.length > 0 ? (
				<div className="form-stack">
					{fields.map((f) => (
						<label className="field" key={f.key}>
							<span>
								{f.label || f.key}
								{f.required ? " *" : ""}
							</span>
							{inputFor(f)}
							{f.description ? (
								<small className="muted">{f.description}</small>
							) : null}
							{f.type === "secret" ? (
								<small className="muted">{t("plugins.configSecretHint")}</small>
							) : null}
						</label>
					))}
				</div>
			) : (
				<label className="field">
					<span>{t("plugins.configJsonLabel")}</span>
					<textarea
						className="mono"
						rows={8}
						value={raw}
						onChange={(e) => setRaw(e.target.value)}
					/>
					<small className="muted">{t("plugins.configJsonHint")}</small>
				</label>
			)}
			{error ? <ErrorState error={error} /> : null}
		</Dialog>
	);
}

function EditPluginDialog({
	row,
	service,
	onClose,
}: {
	row: ModuleStatus;
	service: ReturnType<typeof api>;
	onClose: () => void;
}) {
	const { t } = useI18n();
	const { data: records } = useQuery({
		queryKey: ["plugins"],
		queryFn: ({ signal }) => service.plugins(signal),
		staleTime: 30_000,
	});
	const initial = sidecarOf(
		records?.find((rec: PluginRecord) => rec.id === row.id),
	);
	const [url, setUrl] = useState("");
	const [key, setKey] = useState("");
	const [page, setPage] = useState("");
	const [health, setHealth] = useState("");
	const [prefix, setPrefix] = useState("");
	const [error, setError] = useState("");
	// Back-fill the form once the record arrives (query data is async).
	// Guarded by a ref so later refetches never clobber what the user typed
	// (this is what made the API-key field look untypeable).
	const didBackfill = useRef(false);
	useEffect(() => {
		if (!initial || didBackfill.current) return;
		didBackfill.current = true;
		setUrl(initial.url ?? "");
		setKey(initial.api_key ?? "");
		setPage(initial.page_path ?? "");
		setHealth(initial.health_path ?? "");
		setPrefix(initial.api_prefix ?? "");
	}, [initial]);

	const save = useAdminMutation({
		mutationFn: () =>
			service.updatePlugin(row.id, {
				url: url.trim(),
				apiKey: key.trim(),
				pagePath: page.trim(),
				healthPath: health.trim(),
				apiPrefix: prefix.trim(),
			}),
		invalidateKeys: STORE_INVALIDATE,
		toastOnError: false,
		onSuccess: onClose,
		onError: (err: unknown) => {
			setError(err instanceof Error ? err.message : t("plugins.editFailed"));
		},
	});

	return (
		<Dialog
			title={t("plugins.editTitle")}
			onClose={onClose}
			actions={
				<>
					<Button variant="secondary" onClick={onClose}>
						{t("common.cancel")}
					</Button>
					<Button
						disabled={save.isPending || !url.trim()}
						onClick={() => save.mutate(undefined)}
					>
						{save.isPending ? t("common.working") : t("plugins.saveBtn")}
					</Button>
				</>
			}
		>
			<p className="muted" style={{ fontSize: 12, marginBottom: 10 }}>
				{t("plugins.editHint")}
			</p>
			<div className="form-stack">
				<label className="field">
					<span>{t("plugins.urlLabel")}</span>
					<input
						className="mono"
						value={url}
						onChange={(e) => setUrl(e.target.value)}
						placeholder="http://127.0.0.1:9100"
						spellCheck={false}
					/>
				</label>
				<label className="field">
					<span>{t("plugins.keyPlaceholder")}</span>
					<input
						type="password"
						value={key}
						onChange={(e) => setKey(e.target.value)}
					/>
				</label>
				<div className="form-row">
					<label className="field">
						<span>{t("plugins.pageLabel")}</span>
						<input
							className="mono"
							value={page}
							onChange={(e) => setPage(e.target.value)}
							placeholder="management.html"
							spellCheck={false}
						/>
					</label>
					<label className="field">
						<span>{t("plugins.healthLabel")}</span>
						<input
							className="mono"
							value={health}
							onChange={(e) => setHealth(e.target.value)}
							placeholder="healthz"
							spellCheck={false}
						/>
					</label>
				</div>
				<label className="field">
					<span>{t("plugins.prefixLabel")}</span>
					<input
						className="mono"
						value={prefix}
						onChange={(e) => setPrefix(e.target.value)}
						placeholder="/v0/management"
						spellCheck={false}
					/>
				</label>
				{error ? (
					<p className="muted" style={{ fontSize: 12 }}>
						{error}
					</p>
				) : null}
			</div>
		</Dialog>
	);
}

function CreateChannelDialog({
	row,
	service,
	defaults,
	onClose,
}: {
	row: ModuleStatus;
	service: ReturnType<typeof api>;
	defaults: SidecarConfig | null;
	onClose: () => void;
}) {
	const { t } = useI18n();
	const [name, setName] = useState(`${row.name} 渠道`);
	const [models, setModels] = useState("");
	const [apiKey, setApiKey] = useState(defaults?.api_key ?? "");
	const [error, setError] = useState("");
	const baseUrl = `${defaults?.url ?? ""}${defaults?.channel_path ?? ""}`;

	const create = useAdminMutation({
		mutationFn: () =>
			service.createConnection({
				name: name.trim(),
				base_url: baseUrl,
				secret: apiKey.trim(),
				type_hint: "openai-compatible",
				models_csv: models.trim(),
			}),
		invalidateKeys: [
			["channel-overviews"],
			["channels"],
			["sites"],
			["credentials"],
		],
		toastOnError: false,
		onSuccess: onClose,
		onError: (err: unknown) => {
			setError(err instanceof Error ? err.message : t("plugins.channelFailed"));
		},
	});

	return (
		<Dialog
			title={t("plugins.channelTitle")}
			onClose={onClose}
			actions={
				<>
					<Button variant="secondary" onClick={onClose}>
						{t("common.cancel")}
					</Button>
					<Button
						disabled={
							create.isPending ||
							!name.trim() ||
							!baseUrl.trim() ||
							!apiKey.trim()
						}
						onClick={() => create.mutate(undefined)}
					>
						{create.isPending ? t("common.working") : t("plugins.channelCreate")}
					</Button>
				</>
			}
		>
			<p className="muted" style={{ fontSize: 12, marginBottom: 10 }}>
				{t("plugins.channelHint")}
			</p>
			<div className="form-stack">
				<label className="field">
					<span>{t("plugins.channelName")}</span>
					<input
						value={name}
						onChange={(e) => setName(e.target.value)}
						spellCheck={false}
					/>
				</label>
				<label className="field">
					<span>{t("plugins.channelBase")}</span>
					<input className="mono" value={baseUrl} readOnly spellCheck={false} />
				</label>
				<label className="field">
					<span>{t("plugins.channelModels")}</span>
					<input
						className="mono"
						value={models}
						onChange={(e) => setModels(e.target.value)}
						placeholder="gpt-4o,claude-3-5-sonnet"
						spellCheck={false}
					/>
				</label>
				<label className="field">
					<span>{t("common.secret")}</span>
					<input
						type="password"
						autoComplete="new-password"
						required
						value={apiKey}
						onChange={(e) => setApiKey(e.target.value)}
					/>
				</label>
				{error ? (
					<p className="muted" style={{ fontSize: 12 }}>
						{error}
					</p>
				) : null}
			</div>
		</Dialog>
	);
}

function MarketCard({
	plugin,
	installedVersion,
	busy,
	onInstall,
	t,
}: {
	plugin: MarketPlugin;
	installedVersion?: string;
	busy: boolean;
	onInstall: () => void;
	t: (key: string, vars?: Record<string, string | number>) => string;
}) {
	const installed = installedVersion !== undefined;
	const updateAvailable =
		installed &&
		plugin.version !== undefined &&
		plugin.version !== "" &&
		plugin.version !== installedVersion;
	const kindKey = `store.installKind.${plugin.install?.type ?? "legacy"}`;
	const kindLabel = t(kindKey);
	const kind = kindLabel && kindLabel !== kindKey ? kindLabel : "";
	return (
		<div className="market-card">
			<div className="market-card-head">
				{plugin.logo ? (
					<img
						className="market-card-logo"
						src={plugin.logo}
						alt=""
						onError={(e) => {
							(e.currentTarget as HTMLImageElement).style.display = "none";
						}}
					/>
				) : (
					<div className="market-card-icon">
						<Package size={16} />
					</div>
				)}
				<div className="flex-spacer">
					<strong>{plugin.name}</strong>
					<small className="mono">
						{plugin.id}
						{plugin.version ? ` · v${plugin.version}` : ""}
					</small>
				</div>
				{installed ? <StatusBadge value="enabled" /> : null}
			</div>
			{plugin.description ? (
				<p className="module-card-body">{plugin.description}</p>
			) : null}
			{plugin.tags && plugin.tags.length > 0 ? (
				<div className="market-card-tags">
					{plugin.tags.slice(0, 3).map((tag) => (
						<span key={tag} className="market-card-tag">
							{tag}
						</span>
					))}
				</div>
			) : null}
			<div className="market-card-actions">
				<span className="muted market-card-author">
					{plugin.author ? `${plugin.author} · ` : ""}
					{plugin.source.name}
					{kind ? ` · ${kind}` : ""}
				</span>
				{installed ? (
					updateAvailable ? (
						<Button icon={<RefreshCw size={14} />} disabled={busy} onClick={onInstall}>
							{busy ? t("common.working") : t("store.update")}
						</Button>
					) : (
						<span className="module-core-tag">
							{t("store.installed")}
							{installedVersion ? ` · v${installedVersion}` : ""}
						</span>
					)
				) : (
					<Button icon={<Download size={14} />} disabled={busy} onClick={onInstall}>
						{busy ? t("common.working") : t("store.install")}
					</Button>
				)}
			</div>
		</div>
	);
}

function ModuleCard({
	row,
	busy,
	onActivate,
	t,
}: {
	row: ModuleStatus;
	busy: boolean;
	onActivate?: () => void;
	t: (key: string, vars?: Record<string, string | number>) => string;
}) {
	const description = moduleDescription(row, t);
	const unlockLabels = (row.unlocks ?? []).map((key) => unlockLabel(key, t));
	const badge = !row.installed ? "available" : row.enabled ? "enabled" : "disabled";

	return (
		<div className="module-card">
			<div className="module-card-head">
				<div className="module-card-icon">
					<Package size={18} />
				</div>
				<div className="flex-spacer">
					<strong>{row.name}</strong>
					<small className="mono">
						{row.id} · {row.version}
					</small>
				</div>
				<StatusBadge value={badge} />
			</div>
			<p className="module-card-body">{description}</p>
			{unlockLabels.length > 0 ? (
				<ul className="module-unlocks">
					{unlockLabels.map((label) => (
						<li key={label}>{label}</li>
					))}
				</ul>
			) : null}
			{row.can_toggle && !row.enabled ? (
				<Button icon={<Power size={16} />} disabled={busy} onClick={onActivate}>
					{t("store.activate")}
				</Button>
			) : null}
		</div>
	);
}

function moduleDescription(
	row: ModuleStatus,
	t: (key: string, vars?: Record<string, string | number>) => string,
) {
	const key = `store.module.${row.id}`;
	const localized = t(key);
	if (localized && localized !== key) return localized;
	return row.description || t("store.noDescription");
}

function unlockLabel(
	key: string,
	t: (key: string, vars?: Record<string, string | number>) => string,
) {
	const i18nKey = `store.unlock.${key}`;
	const localized = t(i18nKey);
	if (localized && localized !== i18nKey) return localized;
	return key;
}
