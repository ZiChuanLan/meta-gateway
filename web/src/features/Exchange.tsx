import {
	CheckCircle2,
	Download,
	FileJson,
	Search,
	ShieldAlert,
	Upload,
	X,
} from "lucide-react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { api } from "../api/client";
import type {
	ImportResult,
	WebDAVSettings,
	WebDAVSyncDirection,
	WebDAVSyncMode,
	WebDAVSyncResult,
} from "../api/types";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { useModules } from "../hooks/useModules";
import {
	Button,
	Dialog,
	ErrorState,
	Field,
	InfoTip,
	Page,
	Panel,
	StatusBadge,
} from "../components/ui";
import { useToast } from "../toast";
import {
	scheduleFromSettings,
	settingsFromSchedule,
	type WebDAVSchedulePresetId,
} from "../lib/webdavSchedule";

const MAX_IMPORT_BYTES = 10 * 1024 * 1024;

type Preview = {
	kind: "canonical" | "compatibility";
	format: string;
	version: string;
	items: number;
	importable: boolean | null;
};

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

function previewDocument(doc: unknown): Preview {
	if (Array.isArray(doc)) {
		return {
			kind: "compatibility",
			format: "new-api-array",
			version: "-",
			items: doc.length,
			importable: true,
		};
	}
	if (!isRecord(doc)) {
		return {
			kind: "compatibility",
			format: "unknown",
			version: "-",
			items: 0,
			importable: null,
		};
	}
	if (typeof doc.format === "string") {
		const items = Array.isArray(doc.items) ? doc.items.length : 0;
		return {
			kind: "canonical",
			format: doc.format,
			version: doc.version == null ? "-" : String(doc.version),
			items,
			importable: doc.importable === true,
		};
	}
	if (Array.isArray(doc.channels)) {
		return {
			kind: "compatibility",
			format: "new-api.channels",
			version: "-",
			items: doc.channels.length,
			importable: true,
		};
	}
	if (Array.isArray(doc.data)) {
		return {
			kind: "compatibility",
			format: "new-api.data",
			version: "-",
			items: doc.data.length,
			importable: true,
		};
	}
	// All API Hub V2: profiles preferred; accounts.access_token is the common full backup shape.
	if (doc.version === "2.0" && (isRecord(doc.apiCredentialProfiles) || isRecord(doc.accounts) || Array.isArray(doc.accounts))) {
		const aah = isRecord(doc.apiCredentialProfiles) ? doc.apiCredentialProfiles : null;
		const profiles = aah && Array.isArray(aah.profiles) ? aah.profiles : [];
		let items = profiles.length;
		if (items === 0) {
			const accountRoot = doc.accounts;
			const accountList = Array.isArray(accountRoot)
				? accountRoot
				: isRecord(accountRoot) && Array.isArray(accountRoot.accounts)
					? accountRoot.accounts
					: [];
			items = accountList.filter((entry) => {
				if (!isRecord(entry) || entry.disabled === true) return false;
				const info = isRecord(entry.account_info) ? entry.account_info : null;
				const token =
					(info && (info.access_token || info.apiKey || info.api_key || info.token)) ||
					entry.apiKey ||
					entry.api_key ||
					entry.key ||
					entry.access_token;
				return typeof token === "string" && token.trim().length > 0;
			}).length;
		}
		return {
			kind: "compatibility",
			format: "all-api-hub-v2",
			version: String(doc.version),
			items,
			importable: items > 0,
		};
	}
	return {
		kind: "compatibility",
		format: "unknown",
		version: "-",
		items: 0,
		importable: null,
	};
}

export function Exchange({ embedded = false }: { embedded?: boolean } = {}) {
	const { client } = useSession();
	const { t } = useI18n();
	const { exchangeEnabled, ready: modulesReady } = useModules();
	const s = api(client!);
	const qc = useQueryClient();
	const channels = useQuery({
		queryKey: ["channels"],
		queryFn: ({ signal }) => s.channels(signal),
	});
	const [selected, setSelected] = useState<number[]>([]);
	const [channelQuery, setChannelQuery] = useState("");
	const [secretWarning, setSecretWarning] = useState(false);
	const [fileName, setFileName] = useState("");
	const [fileSize, setFileSize] = useState(0);
	const [document, setDocument] = useState<unknown>(null);
	const [parseError, setParseError] = useState<string | null>(null);
	const [dragging, setDragging] = useState(false);
	const [importResult, setImportResult] = useState<ImportResult | null>(null);
	const [webdavDownloadResult, setWebdavDownloadResult] = useState<WebDAVSyncResult | null>(null);
	const [webdavUploadResult, setWebdavUploadResult] = useState<WebDAVSyncResult | null>(null);
	const [webdavSyncMode, setWebdavSyncMode] = useState<WebDAVSyncMode>("incremental");
	const [confirmReplaceSync, setConfirmReplaceSync] = useState(false);
	const [webdavForm, setWebdavForm] = useState({
		url: "",
		username: "",
		password: "",
		backup_password: "",
		upload_url: "",
		upload_username: "",
		upload_password: "",
		upload_backup_password: "",
		clear_password: false,
		clear_backup_password: false,
		clear_upload_password: false,
		clear_upload_backup_password: false,
		download: false,
		upload: false,
		download_schedule: "off" as WebDAVSchedulePresetId,
		download_cron: "0 */6 * * *",
		upload_schedule: "off" as WebDAVSchedulePresetId,
		upload_cron: "0 */6 * * *",
	});
	type WebDAVSecretKey =
		| "password"
		| "backup_password"
		| "upload_password"
		| "upload_backup_password";
	const [webdavFormHydrated, setWebdavFormHydrated] = useState(false);
	const input = useRef<HTMLInputElement>(null);
	const toast = useToast();

	const filteredChannels = useMemo(() => {
		const q = channelQuery.trim().toLowerCase();
		const list = channels.data ?? [];
		if (!q) return list;
		return list.filter(
			(c) =>
				c.name.toLowerCase().includes(q) ||
				c.base_url.toLowerCase().includes(q) ||
				String(c.id).includes(q) ||
				(c.group_name ?? "").toLowerCase().includes(q),
		);
	}, [channels.data, channelQuery]);

	const exportAll = selected.length === 0;
	const preview = document !== null ? previewDocument(document) : null;

	const exp = useMutation({
		mutationFn: async ({ secrets }: { secrets: boolean }) => {
			const data = await s.exportData(secrets, selected);
			const blob = new Blob([JSON.stringify(data, null, 2)], {
				type: "application/json",
			});
			const url = URL.createObjectURL(blob);
			const a = window.document.createElement("a");
			a.href = url;
			a.download = `meta-gateway-${secrets ? "secret-" : "metadata-"}export.json`;
			a.click();
			URL.revokeObjectURL(url);
			return data;
		},
		onSuccess: (data) => {
			setSecretWarning(false);
			if (data.skipped?.length) {
				toast.push({
					tone: "info",
					message: t("exchange.exportSkipped", {
						n: data.skipped.length,
					}),
				});
			}
		},
	});

	const imp = useMutation({
		mutationFn: () => s.importData(document),
		onSuccess: (result) => {
			setImportResult(result);
			void qc.invalidateQueries({ queryKey: ["sites"] });
			void qc.invalidateQueries({ queryKey: ["credentials"] });
			void qc.invalidateQueries({ queryKey: ["channels"] });
			void qc.invalidateQueries({ queryKey: ["channel-overviews"] });
			void qc.invalidateQueries({ queryKey: ["routes"] });
			void qc.invalidateQueries({ queryKey: ["route-overviews"] });
			void qc.invalidateQueries({ queryKey: ["keys"] });
			void qc.invalidateQueries({ queryKey: ["models"] });
			void qc.invalidateQueries({ queryKey: ["proxy-logs"] });
			void qc.invalidateQueries({ queryKey: ["checkin-logs"] });
			void qc.invalidateQueries({ queryKey: ["audit"] });
			setDocument(null);
			setFileName("");
			setFileSize(0);
			setParseError(null);
			if (input.current) input.current.value = "";
		},
	});

	const webdavStatus = useQuery({
		queryKey: ["webdav-status"],
		queryFn: ({ signal }) => s.webdavStatus(signal),
		enabled: modulesReady && exchangeEnabled,
		retry: (n, err) => {
			const status = (err as { status?: number } | null)?.status;
			if (status === 404) return false;
			return n < 2;
		},
	});
	const webdavSettings = useQuery({
		queryKey: ["webdav-settings"],
		queryFn: ({ signal }) => s.webdavSettings(signal),
		enabled: modulesReady && exchangeEnabled,
		retry: (n, err) => {
			const status = (err as { status?: number } | null)?.status;
			if (status === 404) return false;
			return n < 2;
		},
	});

	const applyWebdavSettings = (settings: WebDAVSettings) => {
		const downloadSchedule = scheduleFromSettings(settings.download_cron || "");
		const uploadSchedule = scheduleFromSettings(settings.upload_cron || "");
		setWebdavForm({
			url: settings.url ?? "",
			username: settings.username ?? "",
			password: "",
			backup_password: "",
			upload_url: settings.upload_url ?? "",
			upload_username: settings.upload_username ?? "",
			upload_password: "",
			upload_backup_password: "",
			clear_password: false,
			clear_backup_password: false,
			clear_upload_password: false,
			clear_upload_backup_password: false,
			download: settings.enabled ?? false,
			upload: settings.upload_enabled ?? false,
			download_schedule: downloadSchedule.preset,
			download_cron: downloadSchedule.cron,
			upload_schedule: uploadSchedule.preset,
			upload_cron: uploadSchedule.cron,
		});
	};

	useEffect(() => {
		if (!webdavSettings.data || webdavFormHydrated) return;
		applyWebdavSettings(webdavSettings.data);
		setWebdavFormHydrated(true);
	}, [webdavSettings.data, webdavFormHydrated]);

	const webdavSettingsPayload = () => ({
		enabled: webdavForm.download,
		upload_enabled: webdavForm.upload,
		url: webdavForm.url.trim(),
		username: webdavForm.username,
		password: webdavForm.password,
		backup_password: webdavForm.backup_password,
		upload_url: webdavForm.upload_url.trim(),
		upload_username: webdavForm.upload_username,
		upload_password: webdavForm.upload_password,
		upload_backup_password: webdavForm.upload_backup_password,
		clear_password: webdavForm.clear_password,
		clear_backup_password: webdavForm.clear_backup_password,
		clear_upload_password: webdavForm.clear_upload_password,
		clear_upload_backup_password: webdavForm.clear_upload_backup_password,
		download_cron: settingsFromSchedule({
			preset: webdavForm.download_schedule,
			cron: webdavForm.download_cron,
		}).cron,
		upload_cron: settingsFromSchedule({
			preset: webdavForm.upload_schedule,
			cron: webdavForm.upload_cron,
		}).cron,
	});

	const invalidateAfterWebdavImport = () => {
		void qc.invalidateQueries({ queryKey: ["sites"] });
		void qc.invalidateQueries({ queryKey: ["credentials"] });
		void qc.invalidateQueries({ queryKey: ["channels"] });
		void qc.invalidateQueries({ queryKey: ["channel-overviews"] });
		void qc.invalidateQueries({ queryKey: ["routes"] });
		void qc.invalidateQueries({ queryKey: ["route-overviews"] });
		void qc.invalidateQueries({ queryKey: ["models"] });
		void qc.invalidateQueries({ queryKey: ["webdav-status"] });
		void qc.invalidateQueries({ queryKey: ["webdav-settings"] });
	};

	const webdavSave = useMutation({
		mutationFn: () => s.updateWebdavSettings(webdavSettingsPayload()),
		onSuccess: (settings: WebDAVSettings) => {
			applyWebdavSettings(settings);
			void qc.invalidateQueries({ queryKey: ["webdav-status"] });
			void qc.invalidateQueries({ queryKey: ["webdav-settings"] });
			toast.push({ tone: "success", message: t("exchange.webdavSaved") });
		},
	});

	const persistWebdavForm = async () => {
		const settings = await s.updateWebdavSettings(webdavSettingsPayload());
		applyWebdavSettings(settings);
		void qc.invalidateQueries({ queryKey: ["webdav-status"] });
		void qc.invalidateQueries({ queryKey: ["webdav-settings"] });
		return settings;
	};

	const webdavTest = useMutation({
		// Always persist form first so typed passwords are not ignored.
		mutationFn: async ({ direction }: { direction: WebDAVSyncDirection }) => {
			await persistWebdavForm();
			return s.webdavTest(direction);
		},
		onSuccess: (result, variables) => {
			if (variables.direction === "download") {
				setWebdavDownloadResult(result);
			} else {
				setWebdavUploadResult(result);
			}
			void qc.invalidateQueries({ queryKey: ["webdav-status"] });
		},
	});

	const webdavSync = useMutation({
		mutationFn: async ({
			direction,
			mode,
		}: {
			direction: WebDAVSyncDirection;
			mode: WebDAVSyncMode;
		}) => {
			await persistWebdavForm();
			return s.webdavSync(direction, mode);
		},
		onSuccess: (result, variables) => {
			if (variables.direction === "download") {
				setWebdavDownloadResult(result);
				if (result.import) {
					setImportResult(result.import);
				}
				invalidateAfterWebdavImport();
			} else {
				setWebdavUploadResult(result);
				void qc.invalidateQueries({ queryKey: ["webdav-status"] });
			}
		},
	});

	const updateWebdavSchedule = (
		direction: "download" | "upload",
		preset: WebDAVSchedulePresetId,
	) => {
		setWebdavForm((prev) => {
			if (direction === "download") {
				const mapped = settingsFromSchedule({ preset, cron: prev.download_cron });
				return { ...prev, download_schedule: preset, download_cron: mapped.cron };
			}
			const mapped = settingsFromSchedule({ preset, cron: prev.upload_cron });
			return { ...prev, upload_schedule: preset, upload_cron: mapped.cron };
		});
	};

	const webdavScheduleField = (direction: "download" | "upload") => {
		const schedule =
			direction === "download" ? webdavForm.download_schedule : webdavForm.upload_schedule;
		const cron = direction === "download" ? webdavForm.download_cron : webdavForm.upload_cron;
		return (
			<>
				<Field label={t("exchange.webdavSchedule")} hint={t("exchange.webdavScheduleHint")}>
					<select
						value={schedule}
						onChange={(event) =>
							updateWebdavSchedule(direction, event.target.value as WebDAVSchedulePresetId)
						}
					>
						{(
							[
								"off",
								"hourly",
								"every3h",
								"every6h",
								"every12h",
								"daily",
								"custom",
							] as WebDAVSchedulePresetId[]
						).map((id) => (
							<option key={id} value={id}>
								{t(`exchange.webdavSchedule.${id}`)}
							</option>
						))}
					</select>
				</Field>
				{schedule === "custom" ? (
					<Field
						label={t("exchange.webdavCron")}
						hint={t("exchange.webdavCronHint")}
					>
						<input
							value={cron}
							onChange={(event) =>
								setWebdavForm((prev) =>
									direction === "download"
										? { ...prev, download_cron: event.target.value }
										: { ...prev, upload_cron: event.target.value },
								)
							}
							placeholder="0 */6 * * *"
							className="mono"
						/>
					</Field>
				) : null}
			</>
		);
	};

	// secretField renders one direction's secret input in plain text; typing
	// replaces the stored secret, and clearing the field after editing removes it.
	const secretField = (
		key: WebDAVSecretKey,
		label: string,
		hint: string | undefined,
		hasStored: boolean,
		placeholder?: string,
	) => {
		const clearKey = `clear_${key}` as const;
		return (
			<Field label={label} hint={hint}>
				<input
					type="text"
					value={webdavForm[key]}
					onChange={(event) => {
						const value = event.target.value;
						setWebdavForm((prev) => ({
							...prev,
							[key]: value,
							[clearKey]: value === "",
						}));
					}}
					placeholder={hasStored && !webdavForm[key] ? "••••••••" : placeholder}
					autoComplete="off"
				/>
			</Field>
		);
	};

	const webdavResultFor = (
		direction: "download" | "upload",
	): WebDAVSyncResult | null => {
		const fresh = direction === "download" ? webdavDownloadResult : webdavUploadResult;
		if (fresh) return fresh;
		const status = webdavStatus.data;
		if (!status) return null;
		return direction === "download"
			? (status.last_download ?? null)
			: (status.last_upload ?? null);
	};

	const webdavResultBlock = (direction: "download" | "upload") => {
		const result = webdavResultFor(direction);
		if (!result) return null;
		return (
			<div className="exchange-preview">
				<div className="exchange-preview-head">
					<strong>{t("exchange.webdavLastResult")}</strong>
					<StatusBadge
						value={result.status === "success" ? "ready" : "unavailable"}
					/>
				</div>
				<p className="exchange-panel-note">
					{result.message || result.category || result.status}
					{result.encrypted ? ` · ${t("exchange.webdavEncrypted")}` : ""}
					{result.latency_ms != null ? ` · ${result.latency_ms} ms` : ""}
				</p>
			</div>
		);
	};


	async function choose(file?: File | null) {
		if (!file) return;
		setImportResult(null);
		setFileName(file.name);
		setFileSize(file.size);
		setParseError(null);
		if (file.size > MAX_IMPORT_BYTES) {
			setDocument(null);
			setParseError(t("exchange.fileTooLarge"));
			return;
		}
		try {
			setDocument(JSON.parse(await file.text()));
		} catch {
			setDocument(null);
			setParseError(t("exchange.parseError"));
		}
	}

	function clearFile() {
		setDocument(null);
		setFileName("");
		setFileSize(0);
		setParseError(null);
		if (input.current) input.current.value = "";
	}

	function toggleAllVisible(checked: boolean) {
		const ids = filteredChannels.map((c) => c.id);
		if (!checked) {
			setSelected((prev) => prev.filter((id) => !ids.includes(id)));
			return;
		}
		setSelected((prev) => {
			const next = new Set(prev);
			for (const id of ids) next.add(id);
			return Array.from(next);
		});
	}

	const visibleSelected = filteredChannels.filter((c) =>
		selected.includes(c.id),
	).length;

	const body = (
		<>
			<section className="exchange-support" aria-label={t("exchange.formatSupport")}>
				<div className="exchange-support-head">
					<span className="exchange-support-kicker">{t("exchange.formatSupport")}</span>
					<p>{t("exchange.formatSupportHint")}</p>
				</div>
				<ul className="exchange-format-list">
					<li className="exchange-format-card">
						<span className="exchange-format-badge is-primary">{t("exchange.formatBadge.native")}</span>
						<strong>{t("exchange.formatCanonical")}</strong>
						<code>meta-gateway-aah-exchange · v1</code>
					</li>
					<li className="exchange-format-card">
						<span className="exchange-format-badge">{t("exchange.formatBadge.compat")}</span>
						<strong>{t("exchange.formatNewApi")}</strong>
						<code>channels · data · array</code>
					</li>
					<li className="exchange-format-card">
						<span className="exchange-format-badge">{t("exchange.formatBadge.compat")}</span>
						<strong>{t("exchange.formatAah")}</strong>
						<code>apiCredentialProfiles</code>
					</li>
				</ul>
			</section>


			<section className="exchange-support webdav-panel" aria-label={t("exchange.webdavTitle")}>
				<div className="exchange-support-head">
					<span className="exchange-support-kicker">{t("exchange.webdavTitle")}</span>
					<p>{t("exchange.webdavHint")}</p>
				</div>

				<div className="webdav-directions">
					<div
						className={
							webdavForm.download
								? "webdav-direction-card is-active"
								: "webdav-direction-card"
						}
					>
						<div className="webdav-direction-head">
							<label className="webdav-direction-toggle">
								<input
									type="checkbox"
									checked={webdavForm.download}
									onChange={(event) =>
										setWebdavForm((prev) => ({
											...prev,
											download: event.target.checked,
										}))
									}
								/>
								<strong>{t("exchange.webdavImportTitle")}</strong>
							</label>
							<InfoTip label={t("exchange.webdavImportHint")} />
						</div>
						<div className="webdav-connection">
							<Field label={t("exchange.webdavUrl")} hint={t("exchange.webdavUrlHint")}>
								<input
									value={webdavForm.url}
									onChange={(event) =>
										setWebdavForm((prev) => ({ ...prev, url: event.target.value }))
									}
									placeholder="https://dav.jianguoyun.com/dav/…"
									autoComplete="off"
								/>
							</Field>
							<div className="form-grid">
								<Field label={t("exchange.webdavUsername")}>
									<input
										value={webdavForm.username}
										onChange={(event) =>
											setWebdavForm((prev) => ({
												...prev,
												username: event.target.value,
											}))
										}
										autoComplete="username"
									/>
								</Field>
								{secretField(
									"password",
									t("exchange.webdavPassword"),
									webdavSettings.data?.has_password
										? t("exchange.webdavPasswordKeep")
										: undefined,
									webdavSettings.data?.has_password ?? false,
								)}
							</div>
							{secretField(
								"backup_password",
								t("exchange.webdavBackupPassword"),
								t("exchange.webdavBackupPasswordHint"),
								webdavSettings.data?.has_backup_password ?? false,
								t("exchange.webdavBackupPasswordPlaceholder"),
							)}
						</div>
						{webdavScheduleField("download")}
						<Field
							label={t("exchange.webdavMode")}
							hint={t("exchange.webdavModeHint")}
						>
							<div className="webdav-mode-grid" role="radiogroup" aria-label={t("exchange.webdavMode")}>
								<label className={webdavSyncMode === "incremental" ? "webdav-mode-card is-selected" : "webdav-mode-card"}>
									<input
										type="radio"
										name="webdav-sync-mode"
										value="incremental"
										checked={webdavSyncMode === "incremental"}
										onChange={() => setWebdavSyncMode("incremental")}
									/>
									<span>
										<strong>{t("exchange.webdavMode.incremental")}</strong>
										<InfoTip label={t("exchange.webdavMode.incrementalHint")} />
									</span>
								</label>
								<label className={webdavSyncMode === "replace" ? "webdav-mode-card is-danger is-selected" : "webdav-mode-card is-danger"}>
									<input
										type="radio"
										name="webdav-sync-mode"
										value="replace"
										checked={webdavSyncMode === "replace"}
										onChange={() => setWebdavSyncMode("replace")}
									/>
									<span>
										<strong>{t("exchange.webdavMode.replace")}</strong>
										<InfoTip label={t("exchange.webdavMode.replaceHint")} />
									</span>
								</label>
							</div>
						</Field>
						<div className="webdav-direction-actions">
							<Button
								variant="secondary"
								disabled={
									webdavSave.isPending ||
									webdavTest.isPending ||
									webdavSync.isPending
								}
								onClick={() => webdavTest.mutate({ direction: "download" })}
							>
								{webdavTest.isPending
									? t("common.loading")
									: t("exchange.webdavTestConnection")}
							</Button>
							<Button
								variant={webdavSyncMode === "replace" ? "danger" : "secondary"}
								disabled={
									webdavSave.isPending ||
									webdavTest.isPending ||
									webdavSync.isPending
								}
								onClick={() => {
									if (webdavSyncMode === "replace") {
										setConfirmReplaceSync(true);
										return;
									}
									webdavSync.mutate({ direction: "download", mode: "incremental" });
								}}
							>
								{webdavSync.isPending ? t("common.loading") : t("exchange.webdavImportNow")}
							</Button>
							{webdavSettings.data?.download_configured ? (
								<span className="webdav-ready-pill">{t("exchange.webdavReady")}</span>
							) : (
								<span className="exchange-empty">
									{t("exchange.webdavCardNotConfigured")}
								</span>
							)}
						</div>
						{webdavResultBlock("download")}
					</div>

					<div
						className={
							webdavForm.upload
								? "webdav-direction-card is-active"
								: "webdav-direction-card"
						}
					>
						<div className="webdav-direction-head">
							<label className="webdav-direction-toggle">
								<input
									type="checkbox"
									checked={webdavForm.upload}
									onChange={(event) =>
										setWebdavForm((prev) => ({
											...prev,
											upload: event.target.checked,
										}))
									}
								/>
								<strong>{t("exchange.webdavBackupTitle")}</strong>
							</label>
							<InfoTip label={t("exchange.webdavBackupHint")} />
						</div>
						<div className="webdav-connection">
							<Field label={t("exchange.webdavUrl")} hint={t("exchange.webdavUrlHint")}>
								<input
									value={webdavForm.upload_url}
									onChange={(event) =>
										setWebdavForm((prev) => ({
											...prev,
											upload_url: event.target.value,
										}))
									}
									placeholder="https://dav.jianguoyun.com/dav/…"
									autoComplete="off"
								/>
							</Field>
							<div className="form-grid">
								<Field label={t("exchange.webdavUsername")}>
									<input
										value={webdavForm.upload_username}
										onChange={(event) =>
											setWebdavForm((prev) => ({
												...prev,
												upload_username: event.target.value,
											}))
										}
										autoComplete="username"
									/>
								</Field>
								{secretField(
									"upload_password",
									t("exchange.webdavPassword"),
									webdavSettings.data?.has_upload_password
										? t("exchange.webdavPasswordKeep")
										: undefined,
									webdavSettings.data?.has_upload_password ?? false,
								)}
							</div>
							{secretField(
								"upload_backup_password",
								t("exchange.webdavBackupPassword"),
								t("exchange.webdavBackupPasswordHint"),
								webdavSettings.data?.has_upload_backup_password ?? false,
								t("exchange.webdavBackupPasswordPlaceholder"),
							)}
						</div>
						{webdavScheduleField("upload")}
						<div className="webdav-direction-actions">
							<Button
								variant="secondary"
								disabled={
									webdavSave.isPending ||
									webdavTest.isPending ||
									webdavSync.isPending
								}
								onClick={() => webdavTest.mutate({ direction: "upload" })}
							>
								{webdavTest.isPending
									? t("common.loading")
									: t("exchange.webdavTestConnection")}
							</Button>
							<Button
								variant="secondary"
								disabled={
									webdavSave.isPending ||
									webdavTest.isPending ||
									webdavSync.isPending
								}
								onClick={() =>
									webdavSync.mutate({ direction: "upload", mode: "incremental" })
								}
							>
								{webdavSync.isPending ? t("common.loading") : t("exchange.webdavBackupNow")}
							</Button>
							{webdavSettings.data?.upload_configured ? (
								<span className="webdav-ready-pill">{t("exchange.webdavReady")}</span>
							) : (
								<span className="exchange-empty">
									{t("exchange.webdavCardNotConfigured")}
								</span>
							)}
						</div>
						{webdavResultBlock("upload")}
					</div>
				</div>

				<div className="webdav-actions">
					<Button
						disabled={webdavSave.isPending}
						onClick={() => webdavSave.mutate()}
					>
						{webdavSave.isPending ? t("common.loading") : t("exchange.webdavSave")}
					</Button>
				</div>

				{confirmReplaceSync ? (
					<Dialog
						title={t("exchange.webdavReplaceConfirmTitle")}
						danger
						onClose={() => setConfirmReplaceSync(false)}
						actions={
							<>
								<Button variant="secondary" onClick={() => setConfirmReplaceSync(false)}>
									{t("common.cancel")}
								</Button>
								<Button
									variant="danger"
									disabled={webdavSync.isPending}
									onClick={() => {
										setConfirmReplaceSync(false);
										webdavSync.mutate({ direction: "download", mode: "replace" });
									}}
								>
									{t("exchange.webdavReplaceConfirmAction")}
								</Button>
							</>
						}
					>
						<p>{t("exchange.webdavReplaceConfirmBody")}</p>
					</Dialog>
				) : null}

				{(webdavSave.isError || webdavTest.isError || webdavSync.isError) && (
					<ErrorState
						error={webdavSave.error ?? webdavTest.error ?? webdavSync.error}
					/>
				)}
			</section>

			<div className="exchange-grid">
				<Panel
					title={t("exchange.exportTitle")}
					actions={
						<span className="exchange-count-pill">
							{exportAll
								? t("exchange.allChannels")
								: t("exchange.selectedCount", { n: selected.length })}
						</span>
					}
				>
					<p className="exchange-panel-copy">{t("exchange.exportHint")}</p>
					<p className="exchange-panel-note">{t("exchange.exportCountHint")}</p>

					<div className="exchange-toolbar">
						<label className="exchange-search">
							<Search size={14} aria-hidden="true" />
							<input
								value={channelQuery}
								onChange={(e) => setChannelQuery(e.target.value)}
								placeholder={t("exchange.searchChannels")}
								aria-label={t("exchange.searchChannels")}
							/>
						</label>
						<div className="toolbar">
							<Button
								variant="secondary"
								disabled={!filteredChannels.length}
								onClick={() => toggleAllVisible(true)}
							>
								{t("exchange.selectVisible")}
							</Button>
							<Button
								variant="quiet"
								disabled={!selected.length}
								onClick={() => setSelected([])}
							>
								{t("exchange.clearSelection")}
							</Button>
						</div>
					</div>

					<div className="selection-list">
						<label className="check">
							<input
								type="checkbox"
								checked={exportAll}
								onChange={() => setSelected([])}
							/>
							<span>{t("exchange.allChannels")}</span>
						</label>
						{channels.isPending ? (
							<span className="exchange-empty">{t("common.loading")}</span>
						) : channels.isError ? (
							<ErrorState error={channels.error} retry={() => channels.refetch()} />
						) : !filteredChannels.length ? (
							<span className="exchange-empty">
								{(channels.data?.length ?? 0) === 0
									? t("exchange.noChannels")
									: t("sites.searchEmpty")}
							</span>
						) : (
							filteredChannels.map((c) => (
								<label className="check" key={c.id}>
									<input
										type="checkbox"
										checked={selected.includes(c.id)}
										onChange={(e) =>
											setSelected(
												e.target.checked
													? [...selected, c.id]
													: selected.filter((id) => id !== c.id),
											)
										}
									/>
									<span className="exchange-channel-label">
										<strong>{c.name}</strong>
										<small>
											#{c.id}
											{c.group_name ? ` · ${c.group_name}` : ""}
											{c.status !== "enabled" ? ` · ${c.status}` : ""}
										</small>
									</span>
								</label>
							))
						)}
					</div>

					{!exportAll && filteredChannels.length > 0 ? (
						<p className="exchange-panel-note">
							{t("exchange.selectedCount", { n: visibleSelected })} /{" "}
							{filteredChannels.length}
						</p>
					) : null}

					{exp.error && <ErrorState error={exp.error} />}
					<div className="toolbar exchange-actions">
						<Button
							variant="secondary"
							icon={<Download size={16} />}
							disabled={exp.isPending || channels.isPending}
							onClick={() => exp.mutate({ secrets: false })}
						>
							{t("exchange.downloadMetadata")}
						</Button>
						<Button
							variant="danger"
							icon={<ShieldAlert size={16} />}
							disabled={exp.isPending || channels.isPending}
							onClick={() => setSecretWarning(true)}
						>
							{t("exchange.exportSecrets")}
						</Button>
					</div>
				</Panel>

				<Panel title={t("exchange.importTitle")}>
					<div
						className={[
							"drop-zone",
							dragging ? "is-dragging" : "",
							document !== null ? "is-ready" : "",
						]
							.filter(Boolean)
							.join(" ")}
						onClick={() => input.current?.click()}
						onDragEnter={(e) => {
							e.preventDefault();
							setDragging(true);
						}}
						onDragOver={(e) => {
							e.preventDefault();
							setDragging(true);
						}}
						onDragLeave={(e) => {
							e.preventDefault();
							if (e.currentTarget === e.target) setDragging(false);
						}}
						onDrop={(e) => {
							e.preventDefault();
							setDragging(false);
							void choose(e.dataTransfer.files?.[0]);
						}}
						role="button"
						tabIndex={0}
						onKeyDown={(e) => {
							if (e.key === "Enter" || e.key === " ") {
								e.preventDefault();
								input.current?.click();
							}
						}}
					>
						{document !== null ? (
							<CheckCircle2 size={28} />
						) : (
							<FileJson size={28} />
						)}
						<strong>
							{fileName || t("exchange.chooseFile")}
						</strong>
						<span>
							{fileName
								? `${(fileSize / 1024).toFixed(1)} KiB · ${t("exchange.dropHint")}`
								: t("exchange.dropHint")}
						</span>
						<span className="drop-zone-limit">{t("exchange.maxSize")}</span>
						<input
							ref={input}
							hidden
							type="file"
							accept="application/json,.json"
							onChange={(e) => void choose(e.target.files?.[0])}
						/>
					</div>

					{fileName ? (
						<div className="exchange-file-bar">
							<span className="mono">{fileName}</span>
							<Button
								variant="quiet"
								icon={<X size={14} />}
								onClick={(e) => {
									e.stopPropagation();
									clearFile();
								}}
							>
								{t("exchange.removeFile")}
							</Button>
						</div>
					) : null}

					{parseError && <ErrorState error={parseError} />}

					{preview && (
						<div className="exchange-preview">
							<div className="exchange-preview-head">
								<strong>{t("exchange.previewTitle")}</strong>
								{preview.importable === true ? (
									<StatusBadge value="ready" />
								) : preview.importable === false ? (
									<StatusBadge value="unavailable" />
								) : (
									<StatusBadge value="info" />
								)}
							</div>
							<div className="exchange-preview-grid">
								<div>
									<span>{t("exchange.previewFormat")}</span>
									<strong>
										{preview.kind === "canonical"
											? preview.format
											: t("exchange.previewUnknown")}
									</strong>
								</div>
								<div>
									<span>{t("exchange.previewVersion")}</span>
									<strong>{preview.version}</strong>
								</div>
								<div>
									<span>{t("exchange.previewItems")}</span>
									<strong>{preview.items}</strong>
								</div>
								<div>
									<span>{t("exchange.previewImportable")}</span>
									<strong>
										{preview.importable === true
											? t("exchange.previewYes")
											: preview.importable === false
												? t("exchange.previewNo")
												: t("exchange.previewUnknown")}
									</strong>
								</div>
							</div>
							{preview.importable === false ? (
								<p className="exchange-panel-note">
									{t("exchange.exportHint")}
								</p>
							) : (
								<p className="exchange-panel-note">
									{t("exchange.readyImport")}
								</p>
							)}
						</div>
					)}

					{imp.error && <ErrorState error={imp.error} />}

					<div className="toolbar exchange-actions">
						<Button
							icon={<Upload size={16} />}
							disabled={
								!document ||
								imp.isPending ||
								preview?.importable === false
							}
							onClick={() => {
								imp.reset();
								imp.mutate();
							}}
						>
							{imp.isPending ? t("exchange.importing") : t("exchange.import")}
						</Button>
					</div>
					{imp.isPending ? (
						<p className="exchange-panel-note">{t("exchange.importingHint")}</p>
					) : null}

					{importResult && (
						<div className="import-result">
							<div className="exchange-preview-head">
								<h3>{t("exchange.importComplete")}</h3>
								{(() => {
									// Import always means rows were written. The badge only reflects
									// post-import key sync / model discovery — never reuse channel
									// health "degraded" (shown as 不可达 / Unreachable).
									const postImportIssues =
										(importResult.key_sync_failure_count ?? 0) > 0 ||
										importResult.discovery_failure_count > 0;
									const missingKeys =
										(importResult.missing_api_key_count ?? 0) > 0;
									const badge = postImportIssues
										? "partial"
										: missingKeys
											? "warning"
											: "success";
									return <StatusBadge value={badge} />;
								})()}
							</div>
							<div className="import-result-stats">
								<span>
									{t("exchange.created")}{" "}
									<strong>{importResult.created_count}</strong>
								</span>
								<span>
									{t("exchange.updated")}{" "}
									<strong>{importResult.updated_count}</strong>
									<small className="muted">
										{" "}
										({t("exchange.updatedHint")})
									</small>
								</span>
								<span>
									{t("exchange.adopted")}{" "}
									<strong>{importResult.adopted_count}</strong>
								</span>
								<span>
									{t("exchange.keySyncOK")}{" "}
									<strong>{importResult.key_sync_success_count ?? 0}</strong>
								</span>
								<span>
									{t("exchange.relayReady")}{" "}
									<strong>{importResult.relay_ready_count ?? 0}</strong>
								</span>
								<span>
									{t("exchange.missingApiKey")}{" "}
									<strong>{importResult.missing_api_key_count ?? 0}</strong>
								</span>
							</div>
							<p className="exchange-panel-note">
								{t("exchange.importSavedNote", {
									n: importResult.channel_ids?.length ?? 0,
								})}
							</p>
							{(importResult.key_sync_failure_count ?? 0) > 0 ||
							importResult.discovery_failure_count > 0 ? (
								<p className="exchange-panel-note">
									{t("exchange.importPartialNote", {
										discovery: importResult.discovery_failure_count,
										keySync: importResult.key_sync_failure_count ?? 0,
									})}
								</p>
							) : (importResult.missing_api_key_count ?? 0) > 0 ? (
								<p className="exchange-panel-note">
									{t("exchange.importMissingKeyNote", {
										n: importResult.missing_api_key_count ?? 0,
									})}
								</p>
							) : null}
							{(() => {
								const keyIssues = (importResult.key_sync ?? []).filter(
									(item) =>
										item.status === "failed" ||
										item.category === "keys_masked" ||
										item.category === "empty_token_list",
								);
								const discIssues = (importResult.discovery ?? []).filter(
									(item) =>
										item.status === "failed" ||
										(item.status === "skipped" &&
											item.category !== "needs_api_key"),
								);
								if (!keyIssues.length && !discIssues.length) {
									return (
										<p className="exchange-panel-note">
											{t("exchange.importNoIssues")}
										</p>
									);
								}
								const shownKeys = keyIssues.slice(0, 8);
								const shownDisc = discIssues.slice(0, 8);
								return (
									<div className="import-discovery-list">
										<strong>{t("exchange.importIssues")}</strong>
										<ul>
											{shownKeys.map((item) => (
												<li key={`ks-${item.channel_id}-${item.status}`}>
													<span>
														{t("exchange.issueKeySync")} #{item.channel_id}
													</span>
													<StatusBadge value={item.status} />
													{item.category ? (
														<small className="mono">{item.category}</small>
													) : null}
												</li>
											))}
											{shownDisc.map((item) => (
												<li key={`d-${item.channel_id}-${item.status}`}>
													<span>
														{t("exchange.issueDiscovery")} #{item.channel_id}
													</span>
													<StatusBadge value={item.status} />
													{item.category ? (
														<small className="mono">{item.category}</small>
													) : null}
												</li>
											))}
										</ul>
										{keyIssues.length + discIssues.length > 16 ? (
											<p className="exchange-panel-note">
												{t("exchange.importIssuesMore", {
													n: keyIssues.length + discIssues.length - 16,
												})}
											</p>
										) : null}
									</div>
								);
							})()}
						</div>
					)}
				</Panel>
			</div>

			{secretWarning && (
				<Dialog
					danger
					title={t("exchange.secretTitle")}
					onClose={() => setSecretWarning(false)}
					actions={
						<>
							<Button
								variant="secondary"
								onClick={() => setSecretWarning(false)}
							>
								{t("common.cancel")}
							</Button>
							<Button
								variant="danger"
								disabled={exp.isPending}
								onClick={() => exp.mutate({ secrets: true })}
							>
								{t("exchange.downloadSensitive")}
							</Button>
						</>
					}
				>
					<p>{t("exchange.secretBody")}</p>
				</Dialog>
			)}
		</>
	);

	if (embedded) return <div className="exchange-embedded">{body}</div>;
	return (
		<Page title={t("exchange.title")} description={t("exchange.description")}>
			{body}
		</Page>
	);
}
