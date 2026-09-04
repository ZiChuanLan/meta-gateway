import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import { useNavigate } from "react-router-dom";
import { Check, Copy } from "lucide-react";
import { api } from "../api/client";
import type { ImportResult } from "../api/types";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { Button, Field } from "../components/ui";

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
	const [imported, setImported] = useState<ImportResult | null>(null);
	const [keyName, setKeyName] = useState("default");
	const [keyCreated, setKeyCreated] = useState(false);
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

	const importBackup = useMutation({
		mutationFn: async (doc: unknown) => s.importData(doc),
		onSuccess: (res) => {
			setImported(res);
			setError("");
			// Adoption kicks off discovery server-side; refresh the checklist.
			queryClient.invalidateQueries({ queryKey: ["channel-overviews"] });
		},
		onError: (err) =>
			setError(err instanceof Error ? err.message : String(err)),
	});

	const onImportFile = async (file: File | undefined) => {
		if (!file) return;
		setImported(null);
		setError("");
		try {
			const doc: unknown = JSON.parse(await file.text());
			importBackup.mutate(doc);
		} catch {
			setError(t("wizard.importInvalid"));
		}
	};

	const createKey = useMutation({
		mutationFn: async () => {
			await s.createKey({ name: keyName.trim() || "default" });
		},
		onSuccess: () => {
			setKeyCreated(true);
			setError("");
			queryClient.invalidateQueries({ queryKey: ["keys"] });
		},
		onError: (err) =>
			setError(err instanceof Error ? err.message : String(err)),
	});

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
								<p className="setup-wizard-desc">
									{t("wizard.importHint")}
								</p>
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
								{imported ? (
									<p className="setup-wizard-ok">
										<Check size={13} />{" "}
										{t("wizard.importDone", {
											created: imported.created_count,
											updated: imported.updated_count,
										})}
									</p>
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
								disabled={keyCreated}
							/>
						</Field>
						{keyCreated ? (
							<p className="setup-wizard-ok">
								<Check size={13} /> {t("wizard.keyCreated")}
							</p>
						) : (
							<Button
								disabled={createKey.isPending}
								onClick={() => createKey.mutate()}
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
								{keyCreated ? t("wizard.next") : t("wizard.later")}
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
