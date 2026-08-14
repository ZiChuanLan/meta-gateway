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
	Trash2,
} from "lucide-react";
import { Link } from "react-router-dom";
import { useMemo, useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { api } from "../api/client";
import type { ModuleStatus, PluginRecord } from "../api/types";
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
import { Button, Dialog, Page, StatusBadge } from "../components/ui";

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
		mutationFn: (id: string) => service.installMarketPlugin(id),
		invalidateKeys: STORE_INVALIDATE,
		pendingIdOf: (id) => id,
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
							className="plugin-add-toggle"
							onClick={() => setAdvancedOpen((v) => !v)}
						>
							<ChevronDown
								size={14}
								style={{
									transform: advancedOpen ? "rotate(180deg)" : undefined,
									transition: "transform 120ms ease",
								}}
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
						{addError ? (
							<p className="muted" style={{ fontSize: 12, marginTop: 8 }}>
								{addError}
							</p>
						) : null}
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
								<p className="detail-empty" style={{ color: "var(--danger)" }}>
									{t("store.marketFailed")}
								</p>
							) : (market.data?.plugins ?? []).length === 0 ? (
								<p className="detail-empty">{t("store.marketEmpty")}</p>
							) : (
								<div className="market-grid">
									{(market.data?.plugins ?? []).map((p) => (
										<MarketCard
											key={p.id}
											plugin={p}
											installed={modules.addons.some((m) => m.id === p.id)}
											busy={installMarket.pendingId === p.id}
											onInstall={() => installMarket.mutate(p.id)}
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
											row.source === "sidecar"
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
		</Page>
	);
}

function PluginRow({
	row,
	busy,
	onEdit,
	onChannel,
	onDisable,
	onUninstall,
	t,
}: {
	row: ModuleStatus;
	busy: boolean;
	onEdit?: () => void;
	onChannel?: () => void;
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
	const [siteId, setSiteId] = useState("");
	const [credentialId, setCredentialId] = useState("");
	const [name, setName] = useState(`${row.name} 渠道`);
	const [models, setModels] = useState("");
	const [apiKey, setApiKey] = useState("");
	const [error, setError] = useState("");
	const baseUrl = `${defaults?.url ?? ""}${defaults?.channel_path ?? ""}`;

	const { data: sites } = useQuery({
		queryKey: ["sites"],
		queryFn: ({ signal }) => service.sites(signal),
		staleTime: 30_000,
	});
	const { data: credentials } = useQuery({
		queryKey: ["credentials", siteId],
		queryFn: ({ signal }) =>
			siteId ? service.credentials(Number(siteId), signal) : Promise.resolve([]),
		enabled: !!siteId,
		staleTime: 30_000,
	});
	useEffect(() => {
		setCredentialId("");
	}, [siteId]);

	const create = useAdminMutation({
		mutationFn: () =>
			service.createChannel({
				name: name.trim(),
				base_url: baseUrl,
				site_id: Number(siteId),
				credential_id: Number(credentialId),
				models_csv: models.trim(),
				header_override: apiKey.trim()
					? `Authorization: Bearer ${apiKey.trim()}`
					: undefined,
			}),
		invalidateKeys: [["channel-overviews"], ["channels"]],
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
						disabled={create.isPending || !siteId || !credentialId}
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
				<div className="form-row">
					<label className="field">
						<span>{t("plugins.channelSite")}</span>
						<select
							value={siteId}
							onChange={(e) => setSiteId(e.target.value)}
						>
							<option value="">—</option>
							{(sites ?? []).map((s) => (
								<option key={s.id} value={s.id}>
									{s.name}
								</option>
							))}
						</select>
					</label>
					<label className="field">
						<span>{t("plugins.channelCredential")}</span>
						<select
							value={credentialId}
							onChange={(e) => setCredentialId(e.target.value)}
							disabled={!siteId}
						>
							<option value="">—</option>
							{(credentials ?? []).map((c) => (
								<option key={c.id} value={c.id}>
									{c.kind} #{c.id}
								</option>
							))}
						</select>
					</label>
				</div>
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
					<span>{t("plugins.keyPlaceholder")}</span>
					<input
						type="password"
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
	installed,
	busy,
	onInstall,
	t,
}: {
	plugin: MarketPlugin;
	installed: boolean;
	busy: boolean;
	onInstall: () => void;
	t: (key: string, vars?: Record<string, string | number>) => string;
}) {
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
				<div style={{ flex: 1, minWidth: 0 }}>
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
				{plugin.author ? (
					<span className="muted" style={{ fontSize: 11 }}>
						{plugin.author}
					</span>
				) : null}
				{installed ? (
					<span className="module-core-tag">{t("store.installed")}</span>
				) : (
					<Button
						icon={<Download size={14} />}
						disabled={busy}
						onClick={onInstall}
					>
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
				<div style={{ flex: 1, minWidth: 0 }}>
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
