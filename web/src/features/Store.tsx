import { ExternalLink, Package, Power, PowerOff, RefreshCw, Trash2 } from "lucide-react";
import { Link } from "react-router-dom";
import { useMemo } from "react";
import { api } from "../api/client";
import type { ModuleStatus } from "../api/types";
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
import { Button, Page, StatusBadge } from "../components/ui";

// Only invalidate always-safe keys here. Gated add-on queries (checkin-logs,
// webdav-*) are owned by their panels with enabled: moduleOn — invalidating them
// while the add-on is off causes noisy 404s after Store toggles.
const STORE_INVALIDATE = [
	MODULES_QUERY_KEY,
	["plugins"],
	["plugins-catalog"],
	["channel-overviews"],
	["credentials"],
];

export function Store() {
	const { t } = useI18n();
	const { client } = useSession();
	const service = api(client!);
	const modules = useModules();

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

	const busyId =
		activate.pendingId ?? disable.pendingId ?? uninstall.pendingId ?? null;

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
			<div className="ops-canvas">
				<div className="system-banner">
					<strong>{t("store.bannerTitle")}</strong>
					<p>{t("store.bannerBody")}</p>
				</div>

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
							<div className="module-grid">
								{enabledAddons.map((row) => (
									<ModuleCard
										key={row.id}
										row={row}
										busy={busyId === row.id}
										onDisable={() => disable.mutate(row.id)}
										t={t}
									/>
								))}
							</div>
						)}
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
							<h2>{t("store.section.core")}</h2>
							<p>{t("store.section.coreHint")}</p>
						</header>
						<div className="module-grid">
							{modules.core.map((row) => (
								<ModuleCard key={row.id} row={row} busy={false} t={t} />
							))}
						</div>
					</section>

					{orphans.length > 0 ? (
						<section className="store-section">
							<header className="store-section-head">
								<h2>{t("store.orphans")}</h2>
								<p>{t("store.orphanHint")}</p>
							</header>
							<div className="module-grid">
								{orphans.map((row) => (
									<ModuleCard
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
		</Page>
	);
}

function ModuleCard({
	row,
	busy,
	onActivate,
	onDisable,
	onUninstall,
	t,
}: {
	row: ModuleStatus;
	busy: boolean;
	onActivate?: () => void;
	onDisable?: () => void;
	onUninstall?: () => void;
	t: (key: string, vars?: Record<string, string | number>) => string;
}) {
	const description = moduleDescription(row, t);
	const unlockLabels = (row.unlocks ?? []).map((key) => unlockLabel(key, t));
	const isCore = row.kind === "core";
	const badge = isCore
		? "enabled"
		: !row.installed
			? "available"
			: row.enabled
				? "enabled"
				: "disabled";

	return (
		<div className={`module-card${isCore ? " is-core" : ""}`}>
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
			<div className="module-card-actions">
				{row.can_toggle && !row.enabled ? (
					<Button icon={<Power size={16} />} disabled={busy} onClick={onActivate}>
						{t("store.activate")}
					</Button>
				) : null}
				{row.can_toggle && row.enabled ? (
					<>
						{row.open_path ? (
							<Link className="button button-secondary" to={row.open_path}>
								<ExternalLink size={14} />
								{t("store.openFeature")}
							</Link>
						) : null}
						<Button
							variant="secondary"
							icon={<PowerOff size={16} />}
							disabled={busy}
							onClick={onDisable}
						>
							{t("store.deactivate")}
						</Button>
					</>
				) : null}
				{isCore ? (
					<span className="module-core-tag">{t("store.builtIn")}</span>
				) : null}
				{onUninstall ? (
					<Button
						variant="quiet"
						icon={<Trash2 size={16} />}
						disabled={busy}
						onClick={onUninstall}
					>
						{t("store.uninstall")}
					</Button>
				) : null}
			</div>
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
