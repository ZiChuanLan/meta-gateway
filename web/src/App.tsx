import {
	Activity,
	ArrowLeftRight,
	Boxes,
	Cable,
	CalendarCheck,
	KeyRound,
	LogOut,
	Menu,
	Network,
	Package,
	Puzzle,
	ScrollText,
	Settings,
	X,
	Sword,
	Zap,
	Image,
	Search,
} from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
	Navigate,
	NavLink,
	Route,
	Routes,
	useLocation,
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
import { Dashboard } from "./features/Dashboard";
import { KatanaCanvas } from "./components/KatanaCanvas";
import { createEdgeSparkHost } from "./lib/katanafx";
import { channelHealthState } from "./features/channelHealth";

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

type TransitionPhase = "idle" | "fading" | "sealing" | "revealing" | "sheathing";

type AuthorizedSession = {
	token: string;
	remember: boolean;
	sites: Site[];
};

const SEAL_DURATION = 1400;
const REVEAL_DURATION = 1600;
const REDUCED_REVEAL_DURATION = 160;
const SHEATH_COVER_MS = 380;
const SHEATH_DURATION = 1000;

export function App() {
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
			// Doors start immediately so there is no empty white beat after the desk fades.
			pendingSession.current = authorized;
			setTransitionPhase("sealing");
			schedule(() => {
				const pending = pendingSession.current;
				if (!pending) return;
				connect(pending.token, pending.remember);
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

	return (
		<>
			{client ? (
				<div
					className={`authenticated-stage${transitionPhase === "revealing" ? " is-revealing" : ""}`}
				>
					<Authenticated
						clientKey={client}
						initialSites={bootstrapSites}
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
			<GatewayTransition phase={transitionPhase} />
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

	// Katana Interactive States: Charge & Stance
	const [isFocused, setIsFocused] = useState(false);
	const [chargeRatio, setChargeRatio] = useState(0);

	// 按钮 hover「磨刀」边缘火花（原生 JS，见 katanafx.ts）
	useEffect(() => {
		const btn = document.querySelector<HTMLButtonElement>(".katana-submit-btn");
		if (!btn) return;
		const host = createEdgeSparkHost(btn);
		const enter = () => host.start();
		const leave = () => host.stop();
		btn.addEventListener("pointerenter", enter);
		btn.addEventListener("pointerleave", leave);
		return () => {
			btn.removeEventListener("pointerenter", enter);
			btn.removeEventListener("pointerleave", leave);
			host.dispose();
		};
	}, []);

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
		const reader = new FileReader();
		reader.onload = () => applyBg(String(reader.result ?? ""));
		reader.readAsDataURL(file);
		e.target.value = "";
	};

	useEffect(() => {
		let interval: number;
		if (isFocused || pending || transitioning) {
			interval = window.setInterval(() => {
				setChargeRatio((prev) => Math.min(1, prev + 0.05));
			}, 30);
		} else {
			setChargeRatio(0);
		}
		return () => clearInterval(interval);
	}, [isFocused, pending, transitioning]);

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
	const pageRef = useRef<HTMLDivElement | null>(null);
	const pointerFrame = useRef(0);
	const pointerTarget = useRef({ rx: 0, ry: 0, xPx: 0.5, yPx: 0.5 });
	const pointerCurrent = useRef({ rx: 0, ry: 0, xPx: 0.5, yPx: 0.5 });

	const paintPointer = useCallback(
		(rx: number, ry: number, xPct: number, yPct: number) => {
			const node = pageRef.current;
			if (!node) return;

			// Continuous plane response: light center, shard bias, no zone swaps.
			node.style.setProperty("--pointer-x", `${xPct * 100}%`);
			node.style.setProperty("--pointer-y", `${yPct * 100}%`);
			node.style.setProperty("--pointer-rx", rx.toFixed(4));
			node.style.setProperty("--pointer-ry", ry.toFixed(4));
			node.style.setProperty("--light-bias-x", `${50 + rx * 12}%`);
			node.style.setProperty("--light-bias-y", `${44 + ry * 10}%`);
			node.style.setProperty("--scene-shift-x", `${rx * 10}px`);
			node.style.setProperty("--scene-shift-y", `${ry * 7}px`);
			node.style.setProperty("--scene-tilt-x", `${ry * -1.8}deg`);
			node.style.setProperty("--scene-tilt-y", `${rx * 2.2}deg`);
			node.style.setProperty(
				"--scene-glow",
				`${0.62 + Math.abs(rx) * 0.16 + Math.abs(ry) * 0.1}`,
			);
			node.style.setProperty(
				"--blue-bias",
				`${0.55 + Math.max(0, -rx) * 0.2 + Math.max(0, ry) * 0.1}`,
			);
		},
		[],
	);

	const runPointerLoop = useCallback(() => {
		const target = pointerTarget.current;
		const current = pointerCurrent.current;
		const ease = 0.12;
		current.rx += (target.rx - current.rx) * ease;
		current.ry += (target.ry - current.ry) * ease;
		current.xPx += (target.xPx - current.xPx) * ease;
		current.yPx += (target.yPx - current.yPx) * ease;
		paintPointer(current.rx, current.ry, current.xPx, current.yPx);
		const settled =
			Math.abs(target.rx - current.rx) < 0.001 &&
			Math.abs(target.ry - current.ry) < 0.001 &&
			Math.abs(target.xPx - current.xPx) < 0.0005 &&
			Math.abs(target.yPx - current.yPx) < 0.0005;
		if (settled) {
			pointerFrame.current = 0;
			return;
		}
		pointerFrame.current = window.requestAnimationFrame(runPointerLoop);
	}, [paintPointer]);

	function trackPointer(e: React.PointerEvent<HTMLDivElement>) {
		const bounds = e.currentTarget.getBoundingClientRect();
		const xPct = Math.max(
			0,
			Math.min(1, (e.clientX - bounds.left) / bounds.width),
		);
		const yPct = Math.max(
			0,
			Math.min(1, (e.clientY - bounds.top) / bounds.height),
		);
		const rx = Math.max(-1, Math.min(1, (xPct - 0.5) * 2));
		const ry = Math.max(-1, Math.min(1, (yPct - 0.5) * 2));
		pointerTarget.current = { rx, ry, xPx: xPct, yPx: yPct };
		if (!pointerFrame.current) {
			pointerFrame.current = window.requestAnimationFrame(runPointerLoop);
		}
	}

	function resetPointer() {
		pointerTarget.current = { rx: 0, ry: 0, xPx: 0.5, yPx: 0.5 };
		if (!pointerFrame.current) {
			pointerFrame.current = window.requestAnimationFrame(runPointerLoop);
		}
	}

	useEffect(() => {
		// seed center lighting
		paintPointer(0, 0, 0.5, 0.5);
		return () => {
			if (pointerFrame.current)
				window.cancelAnimationFrame(pointerFrame.current);
		};
	}, [paintPointer]);

	return (
		<div
			ref={pageRef}
			className={`connect-page${transitioning ? " is-routing" : ""}${transitionPhase === "sealing" ? " is-sealing-out" : ""}${isFocused ? " is-focused-blade" : ""}`}
			onPointerMove={trackPointer}
			onPointerLeave={resetPointer}
		>
			{/* High-speed Katana Physics Canvas & Energy Vortex */}
			<KatanaCanvas
				charging={isFocused || pending || transitioning}
				chargeProgress={chargeRatio}
			/>

			{bgUrl ? (
				<div
					className="connect-custom-bg"
					style={{ backgroundImage: `url("${bgUrl}")` }}
				/>
			) : null}

			<div className="connect-ambient" aria-hidden="true">
				<div className="impact-sky" />
				<div className="impact-bloom impact-bloom-blue" />
				<div className="ambient-vignette" />
			</div>
			<div className="connect-stage">
				<header className="connect-editorial">
					<div className="connect-brand">
						<div className="brand-mark" aria-hidden="true">
							<Sword size={20} className="brand-katana-icon" />
						</div>
						<div className="connect-brand-copy">
							<span>META GATEWAY</span>
							<small>OPERATIONS CONSOLE // 先鋒中繼</small>
						</div>
					</div>
					<h1 className="connect-masthead-title">
						{t("app.connect.title")
							.split("")
							.map((ch, i) => (
								<span
									key={i}
									className="masthead-char"
									style={{ "--i": i } as React.CSSProperties}
								>
									{ch === " " ? "\u00A0" : ch}
								</span>
							))}
					</h1>
					<p className="connect-subtitle">{t("app.connect.subtitle")}</p>
					<div className="connect-edition">
						<span>OPENAI-COMPATIBLE RELAY</span>
						<span>MULTI-CHANNEL · RETRY · FAILOVER</span>
					</div>
				</header>
				<section className="connect-panel">
					<div className="connect-panel-frame" aria-hidden="true" />
					<span className="connect-panel-beam" aria-hidden="true" />
					<span className="connect-panel-tag">MG-07 :// BEARER</span>
					<div className="connect-panel-meta">
						<span>ADMIN API</span>
						<strong>BEARER TOKEN</strong>
						<em>REQUIRED</em>
					</div>
					<div className="connect-toolbar">
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
						{error && <div className="inline-error">{error}</div>}
						<Button
							type="submit"
							disabled={pending || transitioning || !token.trim()}
							className="katana-submit-btn"
						>
							<span className="btn-content">
								<Zap size={14} className="btn-blade-icon" />
								{pending || transitioning
									? t("app.connect.connecting")
									: t("app.connect.submit")}
							</span>
							<span className="btn-energy-charge" style={{ transform: `scaleX(${chargeRatio})` }} />
						</Button>
					</form>
					<div className="connect-footer">
						<span>NO COOKIE · NO URL TOKEN · TAB SESSION ONLY</span>
					</div>
				</section>
			</div>
		</div>
	);
}

function GatewayTransition({ phase }: { phase: TransitionPhase }) {
	if (phase === "idle" || phase === "fading") return null;
	if (phase === "sheathing") {
		return (
			<div className="gateway-transition is-sheathing" aria-hidden="true">
				<div className="sheath-veil" />
				<div className="sheath-blade" />
				<div className="sheath-point" />
				<div className="sheath-word">
					<span>SESSION SEALED</span>
					<small>BEARER DISCARDED</small>
				</div>
			</div>
		);
	}
	return (
		<div className={`gateway-transition is-${phase}`} aria-hidden="true">
			<div className="gateway-plane" aria-hidden="true">
				<div className="gateway-plane-line gateway-plane-line-a" />
				<div className="gateway-plane-line gateway-plane-line-b" />
				<div className="gateway-plane-line gateway-plane-line-c" />
				<div className="gateway-plane-line gateway-plane-line-d" />
			</div>
			<div className="gateway-doors">
				<div className="gateway-door gateway-door-left">
					<span>ADMIN / AUTH</span>
					<strong>TOKEN</strong>
					<small>BEARER VERIFIED</small>
				</div>
				<div className="gateway-door gateway-door-right">
					<span>RELAY / ROUTING</span>
					<strong>SITES</strong>
					<small>CONSOLE OPENING</small>
				</div>
			</div>
			<div className="gateway-console-stage">
				<div className="gateway-transition-lock">
					<span>ADMIN SESSION ESTABLISHED</span>
					<strong>
						CONSOLE
						<br />
						ONLINE
					</strong>
					<div>
						<b>OK</b>
						<i>API</i>
					</div>
					<small>SITES · MODELS · KEYS · AUDIT</small>
				</div>
			</div>
			<div className="gateway-transition-axis" />
		</div>
	);
}

function Authenticated({
	clientKey,
	initialSites,
	onUnauthorized,
}: {
	clientKey: object;
	initialSites?: Site[];
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
		<AuthenticatedShell onUnauthorized={onUnauthorized} />
	);
}
function AuthenticatedShell({
	onUnauthorized,
}: {
	onUnauthorized: () => void;
}) {
	const { t } = useI18n();
	const { checkinEnabled, exchangeEnabled, addons } = useModules();
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

	useEffect(() => {
		document.documentElement.classList.remove("dark");
		window.localStorage.removeItem("meta-gateway.theme");
	}, []);

	const location = useLocation();
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
					m.source === "sidecar" &&
					m.installed &&
					m.enabled &&
					!!m.open_path,
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

	const telehealthTone =
		total === 0 ? "idle" : healthy === total ? "ok" : healthy === 0 ? "down" : "warn";

	return (
		<div className="gate-console-shell">
			{consoleBg ? (
				<div
					className="console-custom-bg"
					style={{ backgroundImage: `url("${consoleBg}")` }}
				/>
			) : null}
			<header className="gate-console-deck">
				<div className="deck-identity">
					<div className="brand-mark" aria-hidden="true">
						<Network size={18} />
					</div>
					<div className="deck-identity-copy">
						<strong>META GATEWAY</strong>
						<span>OPERATIONS CONSOLE // {new Date().getFullYear()}</span>
					</div>
				</div>

				<nav className="deck-sector-rail" aria-label={t("app.nav.open")}>
					{paletteNav.map(({ to, label, icon: Icon }) => (
						<NavLink
							key={to}
							to={to}
							end={to === "/"}
							className={({ isActive }) =>
								`deck-sector${isActive || (to === "/settings" && location.pathname.startsWith("/maintain")) ? " active" : ""}`
							}
						>
							<span className="deck-sector-icon">
								<Icon size={15} />
							</span>
							<span className="deck-sector-label">{label}</span>
							<span className="deck-sector-blade" aria-hidden="true" />
						</NavLink>
					))}
				</nav>

				<div className="deck-status-cluster">
					<div className={`deck-telemetry is-${telehealthTone}`} title={t("dashboard.healthyChannelsHint")}>
						<span className="deck-telemetry-dot" />
						<span className="deck-telemetry-read">
							{channelStats.isPending ? "···" : `${healthy}/${total}`}
						</span>
						<span className="deck-telemetry-label">{t("dashboard.healthyChannels")}</span>
					</div>
					<span className="deck-divider" />
					<button
						type="button"
						className="deck-palette-btn"
						onClick={() => setPaletteOpen(true)}
						title="Command Palette (⌘K / Ctrl+K)"
					>
						<Search size={13} />
						<span>{t("command.placeholder")}</span>
						<kbd className="deck-kbd">⌘K</kbd>
					</button>
					<LanguageSwitcher className="deck-lang" />
					<button
						type="button"
						className="deck-exit-btn"
						onClick={onUnauthorized}
						title={t("app.disconnect")}
						aria-label={t("app.disconnect")}
					>
						<LogOut size={14} />
					</button>
				</div>
			</header>

			<main className={`gate-console-viewport${routeAnim ? " route-enter" : ""}`}>
				<Suspense fallback={<Loading />}>
					<Routes>
						<Route index element={<Dashboard />} />
						<Route path="channels" element={<Channels />} />
						<Route path="models/channel/:channelId" element={<ChannelModels />} />
						<Route path="models" element={<Models />} />
						<Route path="keys" element={<Keys />} />
						<Route path="logs" element={<Logs />} />
						<Route path="checkins" element={<Checkins />} />
						<Route path="exchange" element={<ExchangePage />} />
						<Route path="maintain" element={<Maintain />} />
						<Route path="settings" element={<Maintain />} />
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
			</main>

			<CommandPalette
				open={paletteOpen}
				onClose={() => setPaletteOpen(false)}
				nav={paletteNav}
			/>
		</div>
	);
}
