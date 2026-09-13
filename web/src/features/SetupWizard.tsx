import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Check, Copy } from "lucide-react";
import { ApiError, api } from "../api/client";
import type { CreatedDownstreamKey, ImportResult } from "../api/types";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { Button, Field } from "../components/ui";
import { isEncryptedBackup, skipReasonKey } from "../lib/aahBackup";
import { formatErrorMessage } from "../formatError";

const DONE_KEY = "mg.setup-wizard.done";

type Mode = "auto" | "manual";

const STEPS = ["wizard.stepWelcome", "wizard.stepConnection", "wizard.stepKey", "wizard.stepDone"] as const;

/**
 * First-run setup page (/setup): the decision-making counterpart to the
 * spotlight tour. Collects the choices that genuinely belong to day one —
 * the default model sync mode, the first upstream connection, the first
 * downstream key — then hands the operator the relay endpoint. Auto-redirect
 * claims every fresh instance (no channels) once; the flag persists in
 * localStorage, ?wizard=1 or /setup revisits it any time.
 */
export function SetupWizard() {
	const { client } = useSession();
	const s = api(client!);
	const { t } = useI18n();
	const navigate = useNavigate();
	const queryClient = useQueryClient();

	const [step, setStep] = useState(0);
	const [mode, setMode] = useState<Mode | null>(null);
	const [modeSaved, setModeSaved] = useState(false);
	const [connName, setConnName] = useState("");
	const [baseUrl, setBaseUrl] = useState("");
	const [secret, setSecret] = useState("");
	const [channelId, setChannelId] = useState<number | null>(null);
	const [synced, setSynced] = useState(false);
	const [connTab, setConnTab] = useState<"manual" | "aah">("manual");
	const [importMode, setImportMode] = useState<"file" | "webdav">("file");
	const [imported, setImported] = useState<ImportResult | null>(null);
	const [unlockPassword, setUnlockPassword] = useState("");
	const [needsUnlock, setNeedsUnlock] = useState(false);
	const [webdav, setWebdav] = useState({
		url: "",
		username: "",
		password: "",
		backup_password: "",
	});
	const [webdavTested, setWebdavTested] = useState(false);
	const [keyName, setKeyName] = useState("default");
	const [createdKey, setCreatedKey] = useState<CreatedDownstreamKey | null>(null);
	const [keyCopied, setKeyCopied] = useState(false);
	const [error, setError] = useState("");
	const [copied, setCopied] = useState(false);

	const finish = () => {
		localStorage.setItem(DONE_KEY, "1");
		navigate("/", { replace: true });
	};

	const saveMode = useMutation({
		mutationFn: async () => {
			if (!mode) return;
			const settings = await s.runtimeSettings();
			await s.updateRuntimeSettings({
				...settings.editable,
				default_model_sync_mode: mode,
			});
		},
		onSuccess: () => setModeSaved(true),
		onError: (err) =>
			setError(err instanceof Error ? err.message : String(err)),
	});

	const createConnection = useMutation({
		mutationFn: async () => {
			const res = await s.createConnection({
				name: connName.trim() || undefined,
				base_url: baseUrl.trim(),
				secret: secret.trim(),
			});
			return res;
		},
		onSuccess: (res) => {
			setChannelId(res.channel.id);
			setError("");
			queryClient.invalidateQueries({ queryKey: ["channel-overviews"] });
		},
		onError: (err) =>
			setError(err instanceof Error ? err.message : String(err)),
	});

	const syncModels = useMutation({
		mutationFn: async () => {
			if (channelId == null) return;
			await s.refreshChannel(channelId);
		},
		onSuccess: () => {
			setSynced(true);
			setError("");
			queryClient.invalidateQueries({ queryKey: ["channel-overviews"] });
		},
		// A failed sync usually means the upstream key is wrong; non-blocking.
		onError: () => setError(t("wizard.syncFail")),
	});

	const describeError = (err: unknown) =>
		formatErrorMessage(
			err instanceof ApiError || typeof err === "string"
				? err
				: err instanceof Error
					? err.message
					: String(err),
			t,
		);

	const importBackup = useMutation({
		// An AAH backup may be encrypted; unlock it server-side with the same
		// envelope implementation the WebDAV pull uses.
		mutationFn: async (doc: unknown) =>
			isEncryptedBackup(doc)
				? s.importEncryptedData(doc, unlockPassword)
				: s.importData(doc),
		onSuccess: (res) => {
			setImported(res);
			setError("");
			setNeedsUnlock(false);
			// Adoption kicks off discovery server-side; refresh the checklist.
			queryClient.invalidateQueries({ queryKey: ["channel-overviews"] });
		},
		onError: (err) => {
			const message =
				err instanceof Error ? err.message.toLowerCase() : String(err).toLowerCase();
			if (message.includes("unlock password") || message.includes("backup_unlock_required")) {
				setNeedsUnlock(true);
			}
			setError(describeError(err));
		},
	});

	const onImportFile = async (file: File | undefined) => {
		if (!file) return;
		setImported(null);
		setError("");
		setUnlockPassword("");
		setNeedsUnlock(false);
		try {
			const doc: unknown = JSON.parse(await file.text());
			importBackup.mutate(doc);
		} catch {
			setError(t("wizard.importInvalid"));
		}
	};

	// WebDAV import: persist the connection, then pull the backup through the
	// same admin endpoint the Exchange page uses.
	const persistWebdav = () =>
		s.updateWebdavSettings({
			enabled: true,
			upload_enabled: false,
			url: webdav.url.trim(),
			username: webdav.username,
			password: webdav.password,
			backup_password: webdav.backup_password,
			upload_url: "",
			upload_username: "",
			// Scheduling stays off: the wizard connects the drive, it does not
			// silently enable recurring imports.
			download_cron: "off",
			upload_cron: "off",
		});

	const webdavTest = useMutation({
		mutationFn: async () => {
			await persistWebdav();
			return s.webdavTest("download");
		},
		onSuccess: () => {
			setWebdavTested(true);
			setError("");
		},
		onError: (err) => {
			setWebdavTested(false);
			setError(describeError(err));
		},
	});

	const webdavImport = useMutation({
		mutationFn: async () => {
			await persistWebdav();
			return s.webdavSync("download", "incremental");
		},
		onSuccess: (result) => {
			setWebdavTested(true);
			setImported(result.import ?? null);
			setError("");
			queryClient.invalidateQueries({ queryKey: ["channel-overviews"] });
		},
		onError: (err) => setError(describeError(err)),
	});

	const webdavReady = webdav.url.trim().length > 0 && webdav.username.trim().length > 0 && webdav.password.length > 0;

	const createKey = useMutation({
		mutationFn: async (name: string) => s.createKey({ name }),
		onSuccess: (created) => {
			// Keep the one-time plaintext on screen with a fresh name field, so
			// several keys can be created back to back without leaving the wizard.
			setCreatedKey(created);
			setKeyName("");
			setError("");
			queryClient.invalidateQueries({ queryKey: ["keys"] });
		},
		onError: (err) =>
			setError(err instanceof Error ? err.message : String(err)),
	});

	const copyKey = async () => {
		if (!createdKey) return;
		try {
			await navigator.clipboard.writeText(createdKey.token);
			setKeyCopied(true);
			setTimeout(() => setKeyCopied(false), 1500);
		} catch {
			// Clipboard unavailable; the token stays selectable.
		}
	};

	const curl = `curl ${window.location.origin}/v1/chat/completions \\
  -H "Content-Type: application/json" \\
  -H "Authorization: Bearer sk-..." \\
  -d '{"model":"<model>","messages":[{"role":"user","content":"hi"}]}'`;

	const copyCurl = async () => {
		try {
			await navigator.clipboard.writeText(curl);
			setCopied(true);
			setTimeout(() => setCopied(false), 1500);
		} catch {
			// Clipboard unavailable; the text stays selectable.
		}
	};

	return (
		<div className="setup-wizard">
			<div className="setup-wizard-card">
				<div className="setup-wizard-head">
					<strong>{t("wizard.title")}</strong>
					<button type="button" className="setup-wizard-skip" onClick={finish}>
						{t("wizard.skip")}
					</button>
				</div>
				<p className="setup-wizard-subtitle">{t("wizard.subtitle")}</p>
				<div className="setup-wizard-dots" aria-hidden>
					{STEPS.map((key, dot) => (
						<span key={key} className={dot <= step ? "is-on" : ""} />
					))}
				</div>

				{error ? <p className="setup-wizard-error">{error}</p> : null}

				{step === 0 ? (
					<section>
						<h2>{t("wizard.welcomeTitle")}</h2>
						<p className="setup-wizard-desc">{t("wizard.welcomeDesc")}</p>
						<h3>{t("wizard.modeTitle")}</h3>
						<p className="setup-wizard-desc">{t("wizard.modeDesc")}</p>
						{(
							[
								{
									value: "auto" as Mode,
									label: t("channels.syncModeAuto"),
									desc: t("wizard.modeAuto"),
								},
								{
									value: "manual" as Mode,
									label: t("channels.syncModeManual"),
									desc: t("wizard.modeManual"),
								},
							]
						).map((option) => (
							<label
								key={option.value}
								className={`setup-wizard-option${mode === option.value ? " is-active" : ""}`}
							>
								<input
									type="radio"
									name="wizard-mode"
									checked={mode === option.value}
									onChange={() => setMode(option.value)}
								/>
								<span>
									<strong>{option.label}</strong>
									<small>{option.desc}</small>
								</span>
							</label>
						))}
						{modeSaved ? (
							<p className="setup-wizard-ok">
								<Check size={13} /> {t("wizard.modeSaved")}
							</p>
						) : null}
						<div className="setup-wizard-actions">
							<span />
							<Button
								disabled={!mode || saveMode.isPending}
								onClick={() => {
									setError("");
									saveMode.mutate(undefined, {
										onSettled: () => setStep(1),
									});
								}}
							>
								{t("wizard.next")}
							</Button>
						</div>
					</section>
				) : null}

				{step === 1 ? (
					<section>
						<h2>{t("wizard.connTitle")}</h2>
						<p className="setup-wizard-desc">{t("wizard.connDesc")}</p>
						<div className="setup-wizard-tabs" role="tablist">
							<button
								type="button"
								role="tab"
								aria-selected={connTab === "manual"}
								className={connTab === "manual" ? "is-active" : ""}
								onClick={() => setConnTab("manual")}
							>
								{t("wizard.manualTab")}
							</button>
							<button
								type="button"
								role="tab"
								aria-selected={connTab === "aah"}
								className={connTab === "aah" ? "is-active" : ""}
								onClick={() => setConnTab("aah")}
							>
								{t("wizard.importTab")}
							</button>
						</div>
						{connTab === "manual" ? (
							<>
								<Field label={t("wizard.name")}>
									<input
										value={connName}
										onChange={(e) => setConnName(e.target.value)}
										disabled={channelId != null}
										placeholder="my-upstream"
									/>
								</Field>
								<Field label={t("wizard.baseUrl")}>
									<input
										required
										value={baseUrl}
										onChange={(e) => setBaseUrl(e.target.value)}
										disabled={channelId != null}
										placeholder="https://api.example.com"
									/>
								</Field>
								<Field label={t("wizard.secret")}>
									<input
										required
										type="password"
										autoComplete="new-password"
										value={secret}
										onChange={(e) => setSecret(e.target.value)}
										disabled={channelId != null}
									/>
								</Field>
								{channelId != null ? (
									<p className="setup-wizard-ok">
										<Check size={13} /> {t("wizard.connCreated")}
									</p>
								) : (
									<Button
										disabled={
											createConnection.isPending ||
											!baseUrl.trim() ||
											!secret.trim()
										}
										onClick={() => createConnection.mutate()}
									>
										{createConnection.isPending
											? t("common.loading")
											: t("wizard.createConn")}
									</Button>
								)}
								{channelId != null && !synced ? (
									<Button
										variant="secondary"
										disabled={syncModels.isPending}
										onClick={() => syncModels.mutate()}
									>
										{syncModels.isPending
											? t("wizard.syncing")
											: t("wizard.trySync")}
									</Button>
								) : null}
								{synced ? (
									<p className="setup-wizard-ok">
										<Check size={13} /> {t("wizard.synced")}
									</p>
								) : null}
							</>
						) : (
							<>
								<p className="setup-wizard-desc">{t("wizard.importHint")}</p>
								<div
									className="setup-wizard-tabs is-sub"
									role="group"
									aria-label={t("wizard.importTab")}
								>
									<button
										type="button"
										aria-pressed={importMode === "file"}
										className={importMode === "file" ? "is-active" : ""}
										onClick={() => setImportMode("file")}
									>
										{t("wizard.importFromFile")}
									</button>
									<button
										type="button"
										aria-pressed={importMode === "webdav"}
										className={importMode === "webdav" ? "is-active" : ""}
										onClick={() => setImportMode("webdav")}
									>
										{t("wizard.importFromWebdav")}
									</button>
								</div>

								{importMode === "file" ? (
									<>
										<label className="setup-wizard-file">
											<input
												type="file"
												accept="application/json,.json"
												disabled={importBackup.isPending}
												onChange={(e) => {
													onImportFile(e.target.files?.[0]);
													e.currentTarget.value = "";
												}}
											/>
											<span>
												{importBackup.isPending
													? t("common.loading")
													: t("wizard.pickFile")}
											</span>
										</label>
										{needsUnlock ? (
											<Field label={t("exchange.unlockPassword")}>
												<input
													type="password"
													autoComplete="off"
													value={unlockPassword}
													onChange={(e) => setUnlockPassword(e.target.value)}
												/>
											</Field>
										) : null}
									</>
								) : (
									<>
										<p className="setup-wizard-desc">
											{t("wizard.webdavHint")}
										</p>
										<Field label={t("wizard.webdavUrl")}>
											<input
												value={webdav.url}
												onChange={(e) =>
													setWebdav((prev) => ({ ...prev, url: e.target.value }))
												}
												placeholder="https://dav.jianguoyun.com/dav/"
												autoComplete="off"
											/>
										</Field>
										<Field label={t("wizard.webdavUsername")}>
											<input
												value={webdav.username}
												onChange={(e) =>
													setWebdav((prev) => ({
														...prev,
														username: e.target.value,
													}))
												}
												autoComplete="username"
											/>
										</Field>
										<Field
											label={t("wizard.webdavPassword")}
											hint={t("wizard.webdavPasswordHint")}
										>
											<input
												type="password"
												autoComplete="off"
												value={webdav.password}
												onChange={(e) =>
													setWebdav((prev) => ({
														...prev,
														password: e.target.value,
													}))
												}
											/>
										</Field>
										<Field
											label={t("wizard.webdavBackupPassword")}
											hint={t("wizard.webdavBackupPasswordHint")}
										>
											<input
												type="password"
												autoComplete="off"
												value={webdav.backup_password}
												onChange={(e) =>
													setWebdav((prev) => ({
														...prev,
														backup_password: e.target.value,
													}))
												}
											/>
										</Field>
										<div className="setup-wizard-inline-actions">
											<Button
												variant="secondary"
												disabled={!webdavReady || webdavTest.isPending || webdavImport.isPending}
												onClick={() => webdavTest.mutate()}
											>
												{webdavTest.isPending
													? t("common.loading")
													: t("wizard.webdavTest")}
											</Button>
											<Button
												disabled={!webdavReady || webdavTest.isPending || webdavImport.isPending}
												onClick={() => webdavImport.mutate()}
											>
												{webdavImport.isPending
													? t("common.loading")
													: t("wizard.webdavImport")}
											</Button>
										</div>
										{webdavTested && !webdavImport.isSuccess ? (
											<p className="setup-wizard-ok">
												<Check size={13} /> {t("wizard.webdavTestOk")}
											</p>
										) : null}
									</>
								)}

								{imported ? (
									<p className="setup-wizard-ok">
										<Check size={13} />{" "}
										{importMode === "webdav"
											? t("wizard.webdavDone", {
													created: imported.created_count,
													updated: imported.updated_count,
												})
											: t("wizard.importDone", {
													created: imported.created_count,
													updated: imported.updated_count,
												})}
									</p>
								) : webdavImport.isSuccess ? (
									<p className="setup-wizard-ok">
										<Check size={13} /> {t("wizard.webdavSaved")}
									</p>
								) : null}

								{(imported?.skipped?.length ?? 0) > 0 ? (
									<div className="setup-wizard-skipped">
										<p className="setup-wizard-desc">
											{t("wizard.importSkipped", {
												n: imported?.skipped?.length ?? 0,
											})}
										</p>
										<ul>
											{(imported?.skipped ?? []).slice(0, 5).map((row) => (
												<li key={`wiz-skip-${row.index}-${row.reason}`}>
													<span>{row.name || `#${row.index + 1}`}</span>
													<small>
														{t(
															`exchange.skipReason.${skipReasonKey(row.reason)}`,
														)}
													</small>
												</li>
											))}
										</ul>
									</div>
								) : null}
							</>
						)}
						<div className="setup-wizard-actions">
							<Button variant="secondary" onClick={() => setStep(0)}>
								{t("wizard.back")}
							</Button>
							<Button
								variant={
									channelId != null || imported
										? "primary"
										: "secondary"
								}
								onClick={() => {
									setError("");
									setStep(2);
								}}
							>
								{channelId != null || imported
									? t("wizard.next")
									: t("wizard.later")}
							</Button>
						</div>
					</section>
				) : null}

				{step === 2 ? (
					<section>
						<h2>{t("wizard.keyTitle")}</h2>
						<p className="setup-wizard-desc">{t("wizard.keyDesc")}</p>
						<Field label={t("wizard.keyName")}>
							<input
								value={keyName}
								onChange={(e) => setKeyName(e.target.value)}
								placeholder={t("keys.namePlaceholder")}
							/>
						</Field>
						{createdKey ? (
							<>
								<p className="setup-wizard-ok">
									<Check size={13} /> {t("wizard.keyCreated")}
								</p>
								<div className="setup-wizard-curl">
									<div className="setup-wizard-curl-head">
										<span>Bearer</span>
										<button type="button" onClick={copyKey}>
											<Copy size={12} />
											{keyCopied ? t("setup.copied") : t("setup.copy")}
										</button>
									</div>
									<code>{createdKey.token}</code>
								</div>
								<Button
									variant="secondary"
									disabled={createKey.isPending}
									onClick={() => setCreatedKey(null)}
								>
									{t("wizard.createAnother")}
								</Button>
							</>
						) : (
							<Button
								disabled={createKey.isPending}
								onClick={() => createKey.mutate(keyName.trim() || "default")}
							>
								{t("wizard.createKey")}
							</Button>
						)}
						<div className="setup-wizard-actions">
							<Button variant="secondary" onClick={() => setStep(1)}>
								{t("wizard.back")}
							</Button>
							<Button
								variant="secondary"
								onClick={() => {
									setError("");
									setStep(3);
								}}
							>
								{createdKey ? t("wizard.next") : t("wizard.later")}
							</Button>
						</div>
					</section>
				) : null}

				{step === 3 ? (
					<section>
						<h2>{t("wizard.doneTitle")}</h2>
						<p className="setup-wizard-desc">{t("wizard.doneDesc")}</p>
						<div className="setup-wizard-curl">
							<div className="setup-wizard-curl-head">
								<span>curl</span>
								<button type="button" onClick={copyCurl}>
									<Copy size={12} />
									{copied ? t("setup.copied") : t("setup.copy")}
								</button>
							</div>
							<pre>{curl}</pre>
						</div>
						<div className="setup-wizard-actions">
							<Button onClick={finish}>{t("wizard.enter")}</Button>
						</div>
					</section>
				) : null}
			</div>
		</div>
	);
}
