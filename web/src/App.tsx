import {
	Activity,
	ArrowLeftRight,
	Boxes,
	Cable,
	CalendarCheck,
	KeyRound,
	Package,
	Puzzle,
	ScrollText,
	Settings,
	Wand2,
	ArrowRight,
	ShieldCheck,
	Image,
	Moon,
	Sun,
} from "lucide-react";
import { BrandMark } from "./components/BrandMark";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
	Navigate,
	Route,
	Routes,
	useLocation,
	useNavigate,
} from "react-router-dom";
import { lazy, Suspense, useCallback, useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ApiClient, ApiError, api } from "./api/client";
import type { Site } from "./api/types";
import { LanguageSwitcher, useI18n } from "./i18n";
import { useSession } from "./session";
import { useModules } from "./hooks/useModules";
import {
	Button,
	ErrorState,
	Field,
	IconButton,
	Loading,
} from "./components/ui";
import { CommandPalette } from "./components/CommandPalette";
import { ConsoleShell } from "./components/ConsoleShell";
import { Dashboard } from "./features/Dashboard";
import { GuidedTour } from "./features/GuidedTour";
import { SetupWizard } from "./features/SetupWizard";
import { channelHealthState } from "./features/channelHealth";
import { KatanaCanvas } from "./components/KatanaCanvas";
import { GatewayTransition } from "./components/GatewayTransition";
import { createEdgeSparkHost } from "./lib/katanafx";
import { ENTRANCE_CHARGE_MS, ENTRANCE_EXIT_MS, ENTRANCE_REVEAL_MS } from "./lib/entranceMotion";
import { useHiddenPlugins } from "./lib/pluginNav";
import { AppearanceProvider, useAppearance } from "./appearance";

const Channels = lazy(() =>
	import("./features/Channels").then((module) => ({ default: module.Channels })),
);
const ChannelModels = lazy(() =>
	import("./features/ChannelModels").then((module) => ({ default: module.ChannelModels })),
);
const Checkins = lazy(() =>
	import("./features/Checkins").then((module) => ({ default: module.Checkins })),
);
const ExchangePage = lazy(() =>
	import("./features/ExchangePage").then((module) => ({ default: module.ExchangePage })),
);
const Keys = lazy(() =>
	import("./features/Keys").then((module) => ({ default: module.Keys })),
);
const Logs = lazy(() =>
	import("./features/Logs").then((module) => ({ default: module.Logs })),
);
const Maintain = lazy(() =>
	import("./features/Maintain").then((module) => ({ default: module.Maintain })),
);
const Models = lazy(() =>
	import("./features/Models").then((module) => ({ default: module.Models })),
);
const PluginHost = lazy(() =>
	import("./features/PluginHost").then((module) => ({ default: module.PluginHost })),
);
const Store = lazy(() =>
	import("./features/Store").then((module) => ({ default: module.Store })),
);
const Workbench = lazy(() =>
	import("./features/Workbench").then((module) => ({ default: module.default })),
);

type TransitionPhase = "idle" | "fading" | "sealing" | "revealing" | "sheathing";

type AuthorizedSession = {
	token: string;
	remember: boolean;
	sites: Site[];
};

const SEAL_DURATION = ENTRANCE_CHARGE_MS;
const REVEAL_DURATION = ENTRANCE_REVEAL_MS;
const REDUCED_REVEAL_DURATION = 160;
const SHEATH_COVER_MS = 300;
const SHEATH_DURATION = ENTRANCE_EXIT_MS;

export function App() {
	return <AppearanceProvider><GatewayApp /></AppearanceProvider>;
}

function GatewayApp() {
	const { appearance } = useAppearance();
	const { client, connect, disconnect } = useSession();
	const queryClient = useQueryClient();
	const [transitionPhase, setTransitionPhase] =
		useState<TransitionPhase>("idle");
	const [bootstrapSites, setBootstrapSites] = useState<Site[]>();
	const pendingSession = useRef<AuthorizedSession | null>(null);
	const timers = useRef<number[]>([]);

	const clearTransitionTimers = useCallback(() => {
		for (const timer of timers.current) window.clearTimeout(timer);
		timers.current = [];
	}, []);
	const schedule = useCallback((callback: () => void, delay: number) => {
		const timer = window.setTimeout(() => {
			timers.current = timers.current.filter((entry) => entry !== timer);
			callback();
		}, delay);
		timers.current.push(timer);
	}, []);

	useEffect(() => clearTransitionTimers, [clearTransitionTimers]);
	useEffect(() => {
		if (!client) queryClient.clear();
	}, [client, queryClient]);

	const authorize = useCallback(
		(token: string, remember: boolean, sites: Site[]) => {
			if (transitionPhase !== "idle") return;
			const authorized = { token: token.trim(), remember, sites };
			setBootstrapSites(sites);
			if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
				connect(authorized.token, authorized.remember);
				setTransitionPhase("revealing");
				schedule(() => setTransitionPhase("idle"), REDUCED_REVEAL_DURATION);
				return;
			}
			// Authentication has succeeded; the visual sequence now opens the workspace.
			pendingSession.current = authorized;
			setTransitionPhase("sealing");
			schedule(() => {
				const pending = pendingSession.current;
				if (!pending) return;
				connect(pending.token, pending.remember);
				pendingSession.current = null;
				setTransitionPhase("revealing");
				schedule(() => {
					pendingSession.current = null;
					setTransitionPhase("idle");
				}, REVEAL_DURATION);
			}, SEAL_DURATION);
		},
		[connect, schedule, transitionPhase],
	);

	const handleDisconnect = useCallback(() => {
		clearTransitionTimers();
		pendingSession.current = null;
		setBootstrapSites(undefined);
		if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
			setTransitionPhase("idle");
			disconnect();
			return;
		}
		setTransitionPhase("sheathing");
		schedule(() => disconnect(), SHEATH_COVER_MS);
		schedule(() => setTransitionPhase("idle"), SHEATH_DURATION);
	}, [clearTransitionTimers, disconnect, schedule]);

	const finishTransition = useCallback(() => {
		clearTransitionTimers();
		const pending = pendingSession.current;
		pendingSession.current = null;
		// Skip only completes a transition after authentication has succeeded.
		if (transitionPhase === "sheathing") disconnect();
		else if (pending) connect(pending.token, pending.remember);
		setTransitionPhase("idle");
	}, [clearTransitionTimers, connect, disconnect, transitionPhase]);

	return (
		<>
			{client ? (
				<div
					className={`authenticated-stage${transitionPhase === "revealing" ? " is-revealing" : ""}`}
					style={{ animationDuration: `${REVEAL_DURATION}ms` }}
				>
					<Authenticated
						clientKey={client}
						initialSites={bootstrapSites}
						entranceActive={transitionPhase !== "idle"}
						onUnauthorized={handleDisconnect}
					/>
				</div>
			) : (
				<Connect
					onAuthorized={authorize}
					transitioning={transitionPhase !== "idle"}
					transitionPhase={transitionPhase}
				/>
			)}
			{transitionPhase !== "idle" && transitionPhase !== "fading" ? <GatewayTransition appearance={appearance} phase={transitionPhase} onSkip={finishTransition} /> : null}
		</>
	);
}

function Connect({
	onAuthorized,
	transitioning,
	transitionPhase,
}: {
	onAuthorized: (token: string, remember: boolean, sites: Site[]) => void;
	transitioning: boolean;
	transitionPhase: TransitionPhase;
}) {
	const { t } = useI18n();
	const [token, setToken] = useState("");
	const [remember, setRemember] = useState(true);
	const [error, setError] = useState("");
	const [pending, setPending] = useState(false);
	const [needTOTP, setNeedTOTP] = useState(false);
	const [totpCode, setTotpCode] = useState("");

	const [isFocused, setIsFocused] = useState(false);
	const { scheme, toggleScheme, appearance } = useAppearance();
	const loginRef = useRef<HTMLDivElement>(null);
	const loginButtonRef = useRef<HTMLButtonElement>(null);
	useEffect(() => {
		const button = loginButtonRef.current;
		if (!button) return;
		const reduced = window.matchMedia("(prefers-reduced-motion: reduce)");
		const colors = getComputedStyle(document.documentElement);
		const sparks = createEdgeSparkHost(button, [colors.getPropertyValue("--primary").trim() || "#7cc4f0", colors.getPropertyValue("--accent").trim() || "#d4af37", "#ffffff"], 160);
		const start = () => { if (!reduced.matches && !button.disabled && !document.hidden) sparks.start(); };
		const stop = () => sparks.stop();
		button.addEventListener("pointerenter", start);
		button.addEventListener("pointerleave", stop);
		reduced.addEventListener("change", stop);
		document.addEventListener("visibilitychange", stop);
		return () => { sparks.dispose(); button.removeEventListener("pointerenter", start); button.removeEventListener("pointerleave", stop); reduced.removeEventListener("change", stop); document.removeEventListener("visibilitychange", stop); };
	}, [appearance, scheme]);

	// Custom login background (persisted locally, per browser)
	const [bgUrl, setBgUrl] = useState<string>(() => {
		try {
			return localStorage.getItem("mg.login.bg") ?? "";
		} catch {
			return "";
		}
	});
	const [bgOpen, setBgOpen] = useState(false);
	const bgInputRef = useRef<HTMLInputElement | null>(null);
	const bgBtnRef = useRef<HTMLButtonElement | null>(null);
	const [bgPos, setBgPos] = useState<{ top: number; left: number } | null>(null);
	useEffect(() => {
		if (!bgOpen) {
			setBgPos(null);
			return;
		}
		const compute = () => {
			const btn = bgBtnRef.current;
			if (!btn) return;
			const r = btn.getBoundingClientRect();
			const w = 280;
			const left = Math.max(
				8,
				Math.min(r.right - w, window.innerWidth - w - 8),
			);
			setBgPos({ top: Math.round(r.bottom + 8), left: Math.round(left) });
		};
		compute();
		window.addEventListener("resize", compute);
		return () => window.removeEventListener("resize", compute);
	}, [bgOpen]);
	const applyBg = (value: string) => {
		const url = value.trim();
		try {
			if (url) localStorage.setItem("mg.login.bg", url);
			else localStorage.removeItem("mg.login.bg");
		} catch {
			// Storage unavailable; background only applies for this session.
		}
		setBgUrl(url);
		setBgOpen(false);
	};
	const onBgFile = (e: React.ChangeEvent<HTMLInputElement>) => {
		const file = e.target.files?.[0];
		if (!file) return;
		// Raw data URLs of photos blow past the localStorage quota, so re-encode
		// through a canvas (WebP first, JPEG fallback) sized for a backdrop.
		// createElement("img") because the lucide Image icon import shadows the
		// global Image constructor in this module.
		const img = document.createElement("img");
		const revoke = () => URL.revokeObjectURL(img.src);
		img.onload = () => {
			revoke();
			const maxSide = 2048;
			const scale = Math.min(1, maxSide / Math.max(img.width, img.height));
			const canvas = document.createElement("canvas");
			canvas.width = Math.max(1, Math.round(img.width * scale));
			canvas.height = Math.max(1, Math.round(img.height * scale));
			const ctx = canvas.getContext("2d");
			if (!ctx) {
				const reader = new FileReader();
				reader.onload = () => applyBg(String(reader.result ?? ""));
				reader.readAsDataURL(file);
				return;
			}
			ctx.drawImage(img, 0, 0, canvas.width, canvas.height);
			let data = canvas.toDataURL("image/webp", 0.85);
			if (!data.startsWith("data:image/webp")) {
				data = canvas.toDataURL("image/jpeg", 0.85);
			}
			if (data.length > 3_500_000) {
				const small = document.createElement("canvas");
				small.width = Math.round(canvas.width * 0.66);
				small.height = Math.round(canvas.height * 0.66);
				small.getContext("2d")?.drawImage(canvas, 0, 0, small.width, small.height);
				const retry = small.toDataURL("image/webp", 0.7);
				data = retry.startsWith("data:image/webp") ? retry : small.toDataURL("image/jpeg", 0.7);
			}
			applyBg(data);
		};
		img.onerror = revoke;
		img.src = URL.createObjectURL(file);
		e.target.value = "";
	};


	async function submit(e: React.FormEvent) {
		e.preventDefault();
		if (!token.trim()) return;
		setPending(true);
		setError("");
		try {
			// Unified login exchange: raw token (+ TOTP code when enabled) is
			// swapped for a short-lived signed session token server-side.
			const res = await fetch("/admin/session", {
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({
					token: token.trim(),
					totp_code: needTOTP ? totpCode.trim() : "",
				}),
			});
			const body = await res.json().catch(() => ({}));
			if (res.status === 401 && body.error === "totp_required") {
				setNeedTOTP(true);
				setError(t("app.connect.totpRequired"));
				return;
			}
			if (!res.ok || !body.session_token) {
				setError(
					typeof body.error === "string" && body.error
						? body.error
						: t("app.connect.failed"),
				);
				return;
			}
			const sessionToken = body.session_token as string;
			const sites = await api(new ApiClient(sessionToken)).sites();
			onAuthorized(sessionToken, remember, sites);
		} catch (err) {
			if (err instanceof ApiError) {
				setError(
					err.message === "Unable to reach Meta Gateway" ||
						err.message === "api.unreachable"
						? t("api.unreachable")
						: err.message,
				);
			} else {
				setError(t("app.connect.failed"));
			}
		} finally {
			setPending(false);
		}
	}
	return (
		<div ref={loginRef} className={"login-page login-motion" + (transitionPhase === "sealing" ? " is-leaving" : "")}>
			<div className="login-atmosphere" aria-hidden="true" style={bgUrl ? { backgroundImage: "url(" + JSON.stringify(bgUrl) + ")" } : undefined} />
			<KatanaCanvas charging={isFocused || pending || transitioning} chargeProgress={pending || transitioning ? 1 : .65} motionTarget={loginRef} />
			<div className="login-light-ribbons" aria-hidden="true"><i /><i /><i /></div>
			<header className="login-header">
				<div className="login-brand"><span className="console-brand-mark"><BrandMark size={23} /></span><strong>Meta Gateway</strong></div>
					<div className="login-tools">
					<IconButton label={t(scheme === "dark" ? "app.themeLight" : "app.themeDark")} onClick={toggleScheme}>{scheme === "dark" ? <Sun size={17} /> : <Moon size={17} />}</IconButton>
						<LanguageSwitcher />
						<IconButton
							ref={bgBtnRef}
							label={t("app.connect.background")}
							onClick={() => setBgOpen((v) => !v)}
							className={bgOpen ? "is-active" : ""}
						>
							<Image size={16} />
						</IconButton>
						{bgOpen && bgPos
							? createPortal(
									<div
										className="bg-picker"
										style={{ top: bgPos.top, left: bgPos.left }}
									>
										<input
											ref={bgInputRef}
											type="text"
											placeholder={t("app.connect.bgPlaceholder")}
											defaultValue={bgUrl.startsWith("data:") ? "" : bgUrl}
											onKeyDown={(e) => {
												if (e.key === "Enter" && bgInputRef.current) {
													applyBg(bgInputRef.current.value);
												}
											}}
										/>
										<div className="bg-row">
											<button
												className="is-primary"
												onClick={() =>
													bgInputRef.current && applyBg(bgInputRef.current.value)
												}
											>
												{t("app.connect.bgApply")}
											</button>
											<label className="bg-upload-btn">
												<input
													type="file"
													accept="image/*"
													hidden
													onChange={onBgFile}
												/>
												{t("app.connect.bgUpload")}
											</label>
											<button onClick={() => applyBg("")}>
												{t("app.connect.bgClear")}
											</button>
										</div>
									</div>,
									document.body,
								)
							: null}
					</div>

			</header>
			<main className="login-stage">
				<section className="login-introduction">
					<span className="login-edition">{t("shell.workspace")}</span>
					<h1>{t("login.titleFirst")}<br /><span>{t("login.titleSecond")}</span></h1>
					<p>{t("login.description")}</p>
					<div className="login-sculpture" aria-hidden="true"><span className="login-poster-code">01 / CONNECT</span><span className="login-orbit" /><span className="login-tile is-back" /><span className="login-tile is-middle" /><span className="login-tile is-front"><BrandMark size={46} /></span><span className="login-orbit-point" /><span className="login-poster-axis">META / GATEWAY</span></div>
				</section>
				<section className={"login-card" + (isFocused ? " is-focused" : "")} aria-labelledby="login-card-title">
					<div className="login-card-heading"><span className="login-card-mark"><ShieldCheck size={22} strokeWidth={1.4} /></span><h2 id="login-card-title">{t("login.welcome")}</h2><p>{t("login.hint")}</p></div>
					<form onSubmit={submit} aria-busy={pending || transitioning}>
						<Field label={t("app.connect.token")}>
							<input
								autoFocus
								type="password"
								value={token}
								onChange={(e) => setToken(e.target.value)}
								onFocus={() => setIsFocused(true)}
								onBlur={() => setIsFocused(false)}
								autoComplete="current-password"
								disabled={pending || transitioning}
								required
							/>
						</Field>
						{needTOTP ? (
							<Field label={t("app.connect.totp")}>
								<input
									type="text"
									inputMode="numeric"
									pattern="[0-9]{6}"
									maxLength={6}
									value={totpCode}
									onChange={(e) => setTotpCode(e.target.value.replace(/\D/g, ""))}
									onFocus={() => setIsFocused(true)}
									onBlur={() => setIsFocused(false)}
									autoComplete="one-time-code"
									placeholder="123456"
									disabled={pending || transitioning}
									required
								/>
							</Field>
						) : null}
						<label className="check">
							<input
								type="checkbox"
								checked={remember}
								onChange={(e) => setRemember(e.target.checked)}
								disabled={pending || transitioning}
							/>
							<span>{t("app.connect.remember")}</span>
						</label>
						{error && <div className="inline-error" role="alert">{error}</div>}
						<Button
							type="submit"
							disabled={pending || transitioning || !token.trim()}
							className="login-submit"
							ref={loginButtonRef}
						>
							<span className="btn-content">
								<ArrowRight size={16} />
								{pending || transitioning
									? t("app.connect.connecting")
									: t("app.connect.submit")}
							</span>
						</Button>
					</form>
					<p className="login-private"><ShieldCheck size={13} />{t("login.private")}</p>
				</section>
			</main>
			<footer className="login-footer"><span>Meta Gateway</span><span>{t("login.foundation")}</span></footer>
		</div>
	);
}

function Authenticated({
	clientKey,
	initialSites,
	entranceActive,
	onUnauthorized,
}: {
	clientKey: object;
	initialSites?: Site[];
	entranceActive: boolean;
	onUnauthorized: () => void;
}) {
	const { client } = useSession();
	const { t } = useI18n();
	const auth = useQuery({
		queryKey: ["auth", clientKey],
		queryFn: ({ signal }) => api(client!).sites(signal),
		initialData: initialSites,
	});
	useEffect(() => {
		if (auth.error instanceof ApiError && auth.error.status === 401)
			onUnauthorized();
	}, [auth.error, onUnauthorized]);
	if (auth.isPending)
		return (
			<div className="fullscreen-state">
				<Loading />
			</div>
		);
	if (auth.isError)
		return (
			<div className="fullscreen-state">
				<ErrorState error={auth.error} retry={() => auth.refetch()} />
				<Button variant="secondary" onClick={onUnauthorized}>
					{t("app.disconnect")}
				</Button>
			</div>
		);
	return (
		<AuthenticatedShell onUnauthorized={onUnauthorized} entranceActive={entranceActive} />
	);
}
function AuthenticatedShell({
	onUnauthorized,
	entranceActive,
}: {
	onUnauthorized: () => void;
	entranceActive: boolean;
}) {
	const { t } = useI18n();
	const { checkinEnabled, exchangeEnabled, addons } = useModules();
	// Plugin entries the operator hid from the sidebar (a display preference,
	// stored per browser like the theme).
	const hiddenPlugins = useHiddenPlugins();
	const [paletteOpen, setPaletteOpen] = useState(false);
	const { client } = useSession();
	// Real telemetry: channel health drives the deck readout instead of a static ONLINE.
	const channelStats = useQuery({
		queryKey: ["channel-overviews"],
		queryFn: ({ signal }) => api(client!).channelOverviews(signal),
		refetchInterval: 30_000,
	});
	const healthy = (channelStats.data ?? []).filter((o) =>
		channelHealthState(o) === "healthy",
	).length;
	const total = channelStats.data?.length ?? 0;

	// Build identity: /healthz is public and reports the injected version.
	const [gatewayVersion, setGatewayVersion] = useState("dev");
	useEffect(() => {
		const controller = new AbortController();
		fetch("/healthz", { signal: controller.signal })
			.then((r) => (r.ok ? r.json() : null))
			.then((body: { version?: string } | null) => {
				if (body?.version) setGatewayVersion(body.version);
			})
			.catch(() => {});
		return () => controller.abort();
	}, []);
	const updateCheck = useQuery({
		queryKey: ["update-check"],
		queryFn: ({ signal }) => api(client!).updateCheck(signal),
		staleTime: 10 * 60_000,
		refetchInterval: 30 * 60_000,
	});

	useEffect(() => {
		const onKey = (event: KeyboardEvent) => {
			if ((event.metaKey || event.ctrlKey) && event.key.toLowerCase() === "k") {
				event.preventDefault();
				setPaletteOpen((value) => !value);
			}
		};
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, []);

	const { scheme: theme, toggleScheme: changeTheme, appearance } = useAppearance();

	const location = useLocation();
	const navigate = useNavigate();

	// First-run: a fresh instance (no channels) is claimed by the setup
	// wizard once; the completion/skip flag persists in localStorage.
	useEffect(() => {
		if (import.meta.env.VITEST) return;
		if (location.pathname === "/setup") return;
		if (!channelStats.isSuccess) return;
		if ((channelStats.data?.length ?? 0) !== 0) return;
		if (window.localStorage.getItem("mg.setup-wizard.done") === "1") return;
		navigate("/setup", { replace: true });
	}, [channelStats.isSuccess, channelStats.data, location.pathname, navigate]);

	const [routeAnim, setRouteAnim] = useState(0);
	useEffect(() => {
		setRouteAnim(0);
		const frame = window.requestAnimationFrame(() => setRouteAnim(1));
		return () => window.cancelAnimationFrame(frame);
	}, [location.pathname]);

	// Same persisted custom background as the login page, applied to the console.
	const [consoleBg] = useState<string>(() => {
		try {
			return localStorage.getItem("mg.login.bg") ?? "";
		} catch {
			return "";
		}
	});

	const primaryNav = [
		{ to: "/", label: t("app.nav.overview"), icon: Activity },
		{ to: "/channels", label: t("app.nav.channels"), icon: Cable },
		{ to: "/models", label: t("app.nav.models"), icon: Boxes },
		{ to: "/keys", label: t("app.nav.keys"), icon: KeyRound },
		{ to: "/workbench", label: t("app.nav.workbench"), icon: Wand2 },
		{ to: "/logs", label: t("app.nav.logs"), icon: ScrollText },
		...(checkinEnabled
			? [{ to: "/checkins", label: t("app.nav.checkins"), icon: CalendarCheck }]
			: []),
		...(exchangeEnabled
			? [{ to: "/exchange", label: t("app.nav.exchange"), icon: ArrowLeftRight }]
			: []),
		...(addons
			.filter(
				(m) =>
					// Every enabled sidecar plugin gets a nav entry, whatever brought
					// it here — hand-registered ("sidecar") or market-installed
					// ("market:…"). Gating on the source string made market installs
					// invisible in the sidebar no matter what the plugin's own page
					// toggle said.
					m.installed &&
					m.enabled &&
					!!m.open_path &&
					(m.source === "sidecar" || m.source?.startsWith("market:")) &&
					// A plugin whose entry the operator hid keeps working (its hooks
					// still run); only the sidebar row goes away.
					!hiddenPlugins.has(m.id),
			)
			.map((m) => ({ to: m.open_path!, label: m.name, icon: Puzzle }))),
		{ to: "/store", label: t("app.nav.store"), icon: Package },
	];

	const settingsNav = {
		to: "/settings",
		label: t("app.nav.settings"),
		icon: Settings,
	};

	const paletteNav = [...primaryNav, settingsNav];

	const corePaths = ["/", "/channels", "/models", "/keys"];
	const activityPaths = ["/workbench", "/logs", "/checkins"];
	const navSections = [
		{ label: t("shell.section.gateway"), items: primaryNav.filter((item) => corePaths.includes(item.to)) },
		{ label: t("shell.section.activity"), items: primaryNav.filter((item) => activityPaths.includes(item.to)) },
		{ label: t("shell.section.manage"), items: [...primaryNav.filter((item) => !corePaths.includes(item.to) && !activityPaths.includes(item.to)), settingsNav] },
	];
	return (
		<>
			<ConsoleShell appearance={appearance} sections={navSections} version={gatewayVersion} theme={theme} onThemeChange={changeTheme}
				onSearch={() => setPaletteOpen(true)} onDisconnect={onUnauthorized}
				health={{ healthy, total, loading: channelStats.isPending }}
				update={updateCheck.data?.has_update ? updateCheck.data : undefined} background={consoleBg} entering={Boolean(routeAnim)}>
				<Suspense fallback={<Loading />}>
					<Routes>
						<Route index element={<Dashboard />} />
						<Route path="setup" element={<SetupWizard />} />
						<Route path="channels" element={<Channels />} />
						<Route path="models/channel/:channelId" element={<ChannelModels />} />
						<Route path="models" element={<Models />} />
						<Route path="keys" element={<Keys />} />
					<Route path="workbench" element={<Workbench />} />
						<Route path="logs" element={<Logs />} />
						<Route path="checkins" element={<Checkins />} />
						<Route path="exchange" element={<ExchangePage />} />
						<Route path="settings" element={<Maintain />} />
						{/* /maintain was the original path for this page. It is kept as a
						    redirect (not a second mount) so one page has one URL: two live
						    routes forked browser history, bookmarks and deep links. The
						    search string carries over so /maintain?tab=backups still lands
						    on the right tab. */}
						<Route
							path="maintain"
							element={<Navigate to={`/settings${location.search}`} replace />}
						/>
						<Route path="store" element={<Store />} />
						<Route path="plugins/:id" element={<PluginHost />} />
						<Route path="sites/*" element={<Navigate to="/" replace />} />
						<Route path="routing" element={<Navigate to="/models" replace />} />
						<Route
							path="operations"
							element={<Navigate to="/logs?tab=discovery" replace />}
						/>
						<Route path="assets" element={<Navigate to="/" replace />} />
						<Route path="dashboard" element={<Navigate to="/" replace />} />
						<Route path="*" element={<Navigate to="/" replace />} />
					</Routes>
				</Suspense>

			</ConsoleShell>
			<CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} nav={paletteNav} />
			<GuidedTour enabled={!entranceActive} />
		</>
	);
}
