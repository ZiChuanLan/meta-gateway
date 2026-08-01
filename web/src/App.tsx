import {
	Activity,
	Boxes,
	Cable,
	CalendarCheck,
	KeyRound,
	LogOut,
	Menu,
	Moon,
	Network,
	Package,
	ScrollText,
	Settings,
	Sun,
	X,
} from "lucide-react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import {
	Navigate,
	NavLink,
	Route,
	Routes,
	useLocation,
} from "react-router-dom";
import { useCallback, useEffect, useRef, useState } from "react";
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
	StatusBadge,
} from "./components/ui";
import { Channels } from "./features/Channels";
import { Dashboard } from "./features/Dashboard";
import { ChannelModels } from "./features/ChannelModels";
import { Checkins } from "./features/Checkins";
import { Keys } from "./features/Keys";
import { Logs } from "./features/Logs";
import { Maintain } from "./features/Maintain";
import { Models } from "./features/Models";
import { Store } from "./features/Store";

type TransitionPhase = "idle" | "fading" | "sealing" | "revealing";

type AuthorizedSession = {
	token: string;
	remember: boolean;
	sites: Site[];
};

const SEAL_DURATION = 1400;
const REVEAL_DURATION = 1600;
const REDUCED_REVEAL_DURATION = 160;

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
		setTransitionPhase("idle");
		disconnect();
	}, [clearTransitionTimers, disconnect]);

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
	async function submit(e: React.FormEvent) {
		e.preventDefault();
		if (!token.trim()) return;
		setPending(true);
		setError("");
		try {
			const sites = await api(new ApiClient(token.trim())).sites();
			onAuthorized(token, remember, sites);
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
			className={`connect-page${transitioning ? " is-routing" : ""}${transitionPhase === "sealing" ? " is-sealing-out" : ""}`}
			onPointerMove={trackPointer}
			onPointerLeave={resetPointer}
		>
			<div className="connect-ambient" aria-hidden="true">
				<div className="impact-sky" />
				<div className="impact-bloom impact-bloom-blue" />
				<div className="impact-bloom impact-bloom-warm" />
				<div className="impact-shard impact-shard-a" />
				<div className="impact-shard impact-shard-b" />
				<div className="impact-shard impact-shard-c" />
				<div className="impact-slash" />
				<div className="impact-giant">ADMIN</div>
				<div className="impact-giant impact-giant-sub">RELAY</div>
				<div className="impact-ring" />
				<div className="impact-orbit">
					<span />
					<span />
					<span />
				</div>
				<div className="impact-stripe impact-stripe-a" />
				<div className="impact-stripe impact-stripe-b" />
				<div className="impact-barcode">
					<span />
					<span />
					<span />
					<span />
					<span />
					<span />
					<span />
					<span />
					<span />
					<span />
					<span />
					<span />
				</div>
				<div className="impact-stamp">
					<span>OPENAI-COMPATIBLE RELAY</span>
					<strong>API</strong>
					<em>MULTI-CHANNEL ROUTING</em>
				</div>
				<div className="impact-chip impact-chip-a">SITES · MULTI-CHANNEL</div>
				<div className="impact-chip impact-chip-b">RETRY · FAILOVER</div>
				<div className="impact-chip impact-chip-c">AUDIT · METRICS</div>
				<div className="impact-code impact-code-main">
					<span className="impact-code-kicker">REQUEST PATH / ADMIN API</span>
					<div className="impact-code-body">
						<span>
							<b>POST</b> /v1/chat/completions
						</span>
						<span>Authorization: Bearer &lt;upstream-key&gt;</span>
						<span>
							<b>route.select</b>(site, model, policy)
						</span>
						<span>retry on upstream error / cooldown</span>
						<span>audit.write(request_id, outcome)</span>
						<span>metrics.observe(latency, status)</span>
					</div>
				</div>
				<div className="impact-code impact-code-side">
					<span className="impact-code-kicker">ADMIN SURFACE / LIVE</span>
					<div className="impact-code-body impact-code-body-row">
						<span>Bearer ADMIN_TOKEN</span>
						<span>
							<b>/admin-ui/</b>
						</span>
						<span>sites · models · keys</span>
						<span>healthz · metrics</span>
					</div>
				</div>
				<div className="impact-mark">
					<span>MG</span>
					<small>META GATEWAY</small>
				</div>
				<div className="ambient-unlock-mid">
					<span>ADMIN BEARER VERIFIED</span>
					<strong>
						SESSION
						<br />
						READY
					</strong>
					<div>
						<b>OK</b>
						<i>SITES</i>
					</div>
					<small>TOKEN STAYS IN MEMORY OR SESSION STORAGE</small>
				</div>
				<div className="ambient-pointer-glow" />
				<div className="ambient-vignette" />
			</div>
			<section className="connect-panel">
				<div className="connect-panel-frame" aria-hidden="true" />
				<div className="connect-panel-meta">
					<span>ADMIN API</span>
					<strong>BEARER TOKEN</strong>
					<em>REQUIRED</em>
				</div>
				<div className="connect-toolbar">
					<LanguageSwitcher />
				</div>
				<div className="connect-brand">
					<div className="brand-mark" aria-hidden="true">
						<Network size={20} />
					</div>
					<div className="connect-brand-copy">
						<span>META GATEWAY</span>
						<small>OPERATIONS CONSOLE</small>
					</div>
				</div>
				<div className="connect-heading">
					<p className="connect-kicker">ADMIN ACCESS</p>
					<h1>{t("app.connect.title")}</h1>
					<p className="connect-subtitle">{t("app.connect.subtitle")}</p>
				</div>
				<form onSubmit={submit} aria-busy={pending || transitioning}>
					<Field label={t("app.connect.token")}>
						<input
							autoFocus
							type="password"
							value={token}
							onChange={(e) => setToken(e.target.value)}
							autoComplete="current-password"
							disabled={pending || transitioning}
							required
						/>
					</Field>
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
					>
						{pending || transitioning
							? t("app.connect.connecting")
							: t("app.connect.submit")}
					</Button>
				</form>
				<div className="connect-footer">
					<small>{t("app.connect.hint")}</small>
					<span>NO COOKIE · NO URL TOKEN · TAB SESSION ONLY</span>
				</div>
			</section>
		</div>
	);
}

function GatewayTransition({ phase }: { phase: TransitionPhase }) {
	if (phase === "idle" || phase === "fading") return null;
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
	const ready = useQuery({
		queryKey: ["ready"],
		queryFn: async () => {
			const response = await fetch("/readyz");
			return response.ok;
		},
		refetchInterval: 30_000,
	});
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
		<AuthenticatedShell
			ready={ready.data === true}
			onUnauthorized={onUnauthorized}
		/>
	);
}

function AuthenticatedShell({
	ready,
	onUnauthorized,
}: {
	ready: boolean;
	onUnauthorized: () => void;
}) {
	const { t } = useI18n();
	const { checkinEnabled } = useModules();
	const [open, setOpen] = useState(false);
	const [theme, setTheme] = useState<"light" | "dark">(() => {
		const stored = window.localStorage.getItem("meta-gateway.theme");
		return stored === "dark" ? "dark" : "light";
	});
	useEffect(() => {
		document.documentElement.classList.toggle("dark", theme === "dark");
		window.localStorage.setItem("meta-gateway.theme", theme);
	}, [theme]);
	const location = useLocation();
	const [routeAnim, setRouteAnim] = useState(0);
	useEffect(() => {
		setRouteAnim(0);
		const frame = window.requestAnimationFrame(() => setRouteAnim(1));
		return () => window.cancelAnimationFrame(frame);
	}, [location.pathname]);
	useEffect(() => setOpen(false), [location.pathname]);
	// Daily loop: Connections → Models → Tokens → Logs → Check-in → Store
	const primaryNav = [
		{ to: "/", label: t("app.nav.overview"), icon: Activity },
		{ to: "/channels", label: t("app.nav.channels"), icon: Cable },
		{ to: "/models", label: t("app.nav.models"), icon: Boxes },
		{ to: "/keys", label: t("app.nav.keys"), icon: KeyRound },
		{ to: "/logs", label: t("app.nav.logs"), icon: ScrollText },
		...(checkinEnabled
			? [{ to: "/checkins", label: t("app.nav.checkins"), icon: CalendarCheck }]
			: []),
		{ to: "/store", label: t("app.nav.store"), icon: Package },
	];
	const settingsNav = {
		to: "/settings",
		label: t("app.nav.settings"),
		icon: Settings,
	};
	return (
		<div className="app-shell">
			<header className="mobile-header">
				<IconButton label={t("app.nav.open")} onClick={() => setOpen(true)}>
					<Menu />
				</IconButton>
				<strong>{t("app.brand")}</strong>
				<LanguageSwitcher className="mobile-lang" />
				<StatusBadge value={ready ? "ready" : "unavailable"} />
			</header>
			<aside className={open ? "sidebar open" : "sidebar"}>
				<div className="sidebar-brand">
					<div className="brand-mark" aria-hidden="true">
						<Network size={18} />
					</div>
					<div className="sidebar-brand-copy">
						<strong>{t("app.brand")}</strong>
						<span>{t("app.console")}</span>
					</div>
					<IconButton label={t("app.nav.close")} onClick={() => setOpen(false)}>
						<X />
					</IconButton>
				</div>
				<nav className="sidebar-nav">
					{primaryNav.map(({ to, label, icon: Icon }) => (
						<NavLink key={to} to={to} end={to === "/"}>
							<span className="nav-icon" aria-hidden="true">
								<Icon size={16} />
							</span>
							<span className="nav-label">{label}</span>
						</NavLink>
					))}
				</nav>
				<nav className="sidebar-settings" aria-label={t("app.nav.settings")}>
					<NavLink
						to={settingsNav.to}
						className={({ isActive }) =>
							isActive || location.pathname.startsWith("/maintain")
								? "active"
								: undefined
						}
					>
						<span className="nav-icon" aria-hidden="true">
							<Settings size={16} />
						</span>
						<span className="nav-label">{settingsNav.label}</span>
					</NavLink>
				</nav>
				<div className="sidebar-footer">
					<div className="connection">
						<span className={ready ? "dot healthy" : "dot"} />
						<div>
							<strong>{ready ? t("app.ready") : t("app.notReady")}</strong>
							<span>{t("app.sessionActive")}</span>
						</div>
					</div>
					<LanguageSwitcher className="sidebar-lang" />
					<button
						className="theme-toggle"
						title={theme === "dark" ? t("app.themeLight") : t("app.themeDark")}
						onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
					>
						{theme === "dark" ? <Sun size={16} /> : <Moon size={16} />}
					</button>
					<button onClick={onUnauthorized}>
						<LogOut size={16} />
						{t("app.disconnect")}
					</button>
				</div>
			</aside>
			{open && (
				<button
					className="drawer-scrim"
					aria-label={t("app.nav.close")}
					onClick={() => setOpen(false)}
				/>
			)}
			<div className={`content${routeAnim ? " route-enter" : ""}`}>
				<Routes>
					<Route index element={<Dashboard />} />
					<Route path="channels" element={<Channels />} />
					<Route path="models/channel/:channelId" element={<ChannelModels />} />
					<Route path="models" element={<Models />} />
					<Route path="keys" element={<Keys />} />
					<Route path="logs" element={<Logs />} />
					<Route path="checkins" element={<Checkins />} />
					<Route path="maintain" element={<Maintain />} />
					<Route path="settings" element={<Maintain />} />
					<Route path="store" element={<Store />} />
					{/* Legacy paths map into the channel-first product. */}
					<Route path="sites/*" element={<Navigate to="/" replace />} />
					<Route path="routing" element={<Navigate to="/models" replace />} />
					<Route
						path="operations"
						element={<Navigate to="/logs?tab=discovery" replace />}
					/>
					<Route
						path="exchange"
						element={<Navigate to="/settings?tab=exchange" replace />}
					/>
					<Route path="assets" element={<Navigate to="/" replace />} />
					<Route path="dashboard" element={<Navigate to="/" replace />} />
					<Route path="*" element={<Navigate to="/" replace />} />
				</Routes>
			</div>
		</div>
	);
}
