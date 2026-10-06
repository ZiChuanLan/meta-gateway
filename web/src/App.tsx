import { OperatorUpgradePrompt } from "./features/OperatorProfilePanel";
import {
  ArrowLeft,
  ArrowRight,
  ChevronRight,
  HelpCircle,
  LogIn,
  Puzzle,
  ShieldCheck,
  Ticket,
  Image,
  Moon,
  Sun,
} from "lucide-react";

import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Navigate, Route, Routes, useLocation, useNavigate } from "react-router-dom";
import { lazy, Suspense, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ApiClient, ApiError, api } from "./api/client";
import type { Site } from "./api/types";
import { LanguageSwitcher, useI18n } from "./i18n";
import { useSession, type AccountSession } from "./session";
import { useModules } from "./hooks/useModules";
import { Button, ErrorState, Dialog, Field, IconButton, Loading } from "./components/ui";
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
import {
  CHROME_NAV_ITEMS,
  STAFF_ONLY_PATHS,
  canAccessNav,
  type ChromeNavItem,
} from "./lib/chromeNav";
import { modeHiddenNav, useChromePrefs } from "./lib/topBar";
import { isStaff, type ConsoleRole } from "./session";
import { accountRequest, COOKIE_SESSION, setTeamCSRF } from "./team/transport";
import { teamText } from "./team/text";
import { AcceptFlow } from "./features/AcceptFlow";
import { AccountPage } from "./features/AccountPage";
import { AppearanceProvider, useAppearance } from "./appearance";
import { LoginShell } from "./components/LoginShell";
// The multi-user module (its own area of the console) and the sign-in screen a
// team admin gets. Boards are loaded on demand — see team/panels/lazy.ts.
import * as UsersBoards from "./team/panels/lazy";
import { useOperatingMode } from "./hooks/useOperatingMode";
import { setCurrency, useCurrency } from "./lib/format";
import { useToast } from "./toast";
import { PRELOAD_EXHAUSTED_EVENT } from "./lib/preloadRecovery";

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
  import("./features/Exchange").then((module) => ({ default: module.Exchange })),
);
const Keys = lazy(() => import("./features/Keys").then((module) => ({ default: module.Keys })));
const Logs = lazy(() => import("./features/Logs").then((module) => ({ default: module.Logs })));
const Maintain = lazy(() =>
  import("./features/Maintain").then((module) => ({ default: module.Maintain })),
);
const Models = lazy(() =>
  import("./features/Models").then((module) => ({ default: module.Models })),
);
const PluginHost = lazy(() =>
  import("./features/PluginHost").then((module) => ({ default: module.PluginHost })),
);
const Store = lazy(() => import("./features/Store").then((module) => ({ default: module.Store })));
const Workbench = lazy(() =>
  import("./features/Workbench").then((module) => ({ default: module.default })),
);
const StandaloneAdmin = lazy(() =>
  import("./team/StandaloneAdmin").then((module) => ({ default: module.StandaloneAdmin })),
);
const UsersLayout = UsersBoards.UsersLayout;

type TransitionPhase = "idle" | "fading" | "sealing" | "revealing" | "sheathing";

type AuthorizedSession = {
  token: string;
  remember: boolean;
  sites: Site[];
  account?: AccountSession;
};

const SEAL_DURATION = ENTRANCE_CHARGE_MS;
const REVEAL_DURATION = ENTRANCE_REVEAL_MS;
const REDUCED_REVEAL_DURATION = 160;
const SHEATH_COVER_MS = 300;
const SHEATH_DURATION = ENTRANCE_EXIT_MS;

export function App() {
  return (
    <AppearanceProvider>
      <GatewayApp />
    </AppearanceProvider>
  );
}

function GatewayApp() {
  useCurrency();
  const { appearance } = useAppearance();
  const { client, connect, connectMember, disconnect, role } = useSession();
  // The standalone admin screen renders the shared user-management module, so
  // it passes the transport in — the module deliberately does not know about
  // the console's session (see team/UsersLayout.tsx).
  const adminRequest = useCallback(
    <T,>(path: string, init?: RequestInit) => client!.request<T>(path, init),
    [client],
  );
  const queryClient = useQueryClient();
  const toast = useToast();
  const { t } = useI18n();
  // Money is rendered through the site's currency everywhere, so it is read
  // once here and applied to the shared formatter before any table paints.
  const service = useMemo(() => (client ? api(client) : null), [client]);
  const currencySettings = useQuery({
    queryKey: ["display-settings", role ?? "staff"],
    // The console's own endpoint is behind the admin gate; a member reads
    // the same values from their own path, so both format money alike.
    queryFn: ({ signal }) =>
      role === "member" || role === "admin"
        ? accountRequest<{ symbol: string; rate: number }>("/me/display-settings", { signal })
        : service!.displaySettings(signal),
    enabled: Boolean(service),
  });
  useEffect(() => {
    if (currencySettings.data) setCurrency(currencySettings.data);
  }, [currencySettings.data]);
  const [transitionPhase, setTransitionPhase] = useState<TransitionPhase>("idle");
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
  // The preload recovery reloads the tab once when a chunk went missing after
  // a deploy. If that did not help, this tab is genuinely holding an old
  // build: say so instead of leaving a blank page or a console error.
  useEffect(() => {
    const onStale = () => toast.push({ tone: "error", message: t("app.staleBundle") });
    window.addEventListener(PRELOAD_EXHAUSTED_EVENT, onStale);
    return () => window.removeEventListener(PRELOAD_EXHAUSTED_EVENT, onStale);
  }, [toast, t]);
  useEffect(() => {
    if (!client) queryClient.clear();
  }, [client, queryClient]);

  const adoptSession = useCallback(
    (session: AuthorizedSession) => {
      if (session.account) connectMember(session.account);
      else connect(session.token, session.remember);
    },
    [connect, connectMember],
  );

  const authorize = useCallback(
    (token: string, remember: boolean, sites: Site[], account?: AccountSession) => {
      if (transitionPhase !== "idle") return;
      const authorized = { token: token.trim(), remember, sites, account };
      setBootstrapSites(sites);
      if (window.matchMedia("(prefers-reduced-motion: reduce)").matches) {
        adoptSession(authorized);
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
        adoptSession(pending);
        pendingSession.current = null;
        setTransitionPhase("revealing");
        schedule(() => {
          pendingSession.current = null;
          setTransitionPhase("idle");
        }, REVEAL_DURATION);
      }, SEAL_DURATION);
    },
    [adoptSession, schedule, transitionPhase],
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
    else if (pending) adoptSession(pending);
    setTransitionPhase("idle");
  }, [clearTransitionTimers, adoptSession, disconnect, transitionPhase]);

  return (
    <>
      {client && role === "admin" ? (
        <div>
          <header style={{ padding: 16, display: "flex", justifyContent: "space-between" }}>
            <strong>Meta Gateway</strong>
            <Button onClick={handleDisconnect}>{t("app.disconnect")}</Button>
          </header>
          <Suspense fallback={<Loading />}>
            <StandaloneAdmin request={adminRequest} />
          </Suspense>
        </div>
      ) : client ? (
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
      {transitionPhase !== "idle" && transitionPhase !== "fading" ? (
        <GatewayTransition
          appearance={appearance}
          phase={transitionPhase}
          onSkip={finishTransition}
        />
      ) : null}
    </>
  );
}

function Connect({
  onAuthorized,
  transitioning,
  transitionPhase,
}: {
  onAuthorized: (token: string, remember: boolean, sites: Site[], account?: AccountSession) => void;
  transitioning: boolean;
  transitionPhase: TransitionPhase;
}) {
  const { t, locale } = useI18n();
  const { connectMember } = useSession();
  // Invitation, recovery and code links all land here now: /app used to serve
  // them, and the console is where those links point. The tokens ride in the
  // query string, which is also what makes them survivable across a sign-in
  // redirect.
  const linkParams = new URLSearchParams(location.search);
  const inviteToken = linkParams.get("invite") ?? "";
  const recoveryToken = linkParams.get("recovery") ?? "";
  const [manualCode, setManualCode] = useState(false);
  const accepting = Boolean(inviteToken || recoveryToken || manualCode);
  const team = teamText(locale);
  const [username, setUsername] = useState("");
  const [upgradeHelp, setUpgradeHelp] = useState(false);
  // The second layer of the sign-in card: registration, third-party sign-in and
  // the upgrade guide. Collapsed by default so the form stays the only thing
  // asking for attention.
  const [otherOpen, setOtherOpen] = useState(false);
  const [providers, setProviders] = useState<{ id: string; label: string }[]>([]);
  const [password, setPassword] = useState("");
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
    const sparks = createEdgeSparkHost(
      button,
      [
        colors.getPropertyValue("--primary").trim() || "#7cc4f0",
        colors.getPropertyValue("--accent").trim() || "#d4af37",
        "#ffffff",
      ],
      160,
    );
    const start = () => {
      if (!reduced.matches && !button.disabled && !document.hidden) sparks.start();
    };
    const stop = () => sparks.stop();
    button.addEventListener("pointerenter", start);
    button.addEventListener("pointerleave", stop);
    reduced.addEventListener("change", stop);
    document.addEventListener("visibilitychange", stop);
    return () => {
      sparks.dispose();
      button.removeEventListener("pointerenter", start);
      button.removeEventListener("pointerleave", stop);
      reduced.removeEventListener("change", stop);
      document.removeEventListener("visibilitychange", stop);
    };
  }, [appearance, scheme]);

  // /auth/options is what mints the CSRF cookie the invitation, recovery and
  // third-party flows post against, and it is the only source of the configured
  // providers. Nothing used to call it, which left every one of those POSTs
  // answering 403 csrf_failed and the sign-in page with no third-party buttons
  // at all. A personal deployment answers 404 here; that simply means no
  // providers, and the sign-in form carries on unchanged.
  useEffect(() => {
    const controller = new AbortController();
    void fetch("/auth/options", { credentials: "same-origin", signal: controller.signal })
      .then((response) => (response.ok ? response.json() : null))
      .then((body: { csrf?: string; oauth?: { id: string; label: string }[] } | null) => {
        if (!body) return;
        if (body.csrf) setTeamCSRF(body.csrf);
        if (body.oauth?.length) setProviders(body.oauth);
      })
      .catch(() => {
        /* personal mode, offline, or aborted: the form still works */
      });
    return () => controller.abort();
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
      const left = Math.max(8, Math.min(r.right - w, window.innerWidth - w - 8));
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

  async function submit(e: React.FormEvent, legacy = false) {
    e.preventDefault();
    if ((!legacy && !username.trim()) || !password) return;
    setPending(true);
    setError("");
    try {
      // The server selects the account and role; the client never guesses
      // identity from a username or retries with elevated credentials.
      const res = await fetch("/admin/session", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          ...(legacy ? { token: password } : { username: username.trim(), password }),
          totp_code: needTOTP ? totpCode.trim() : "",
        }),
      });
      const body = await res.json().catch(() => ({}));
      if (res.status === 401 && body.error === "totp_required") {
        setNeedTOTP(true);
        setError(t("app.connect.totpRequired"));
        return;
      }
      if (!res.ok || (!body.session_token && !body.user)) {
        setError(
          typeof body.error === "string" && body.error
            ? body.error === "invalid_credentials"
              ? t("app.connect.invalidCredentials")
              : body.error
            : t("app.connect.failed"),
        );
        return;
      }
      if (body.user && body.csrf) {
        onAuthorized(COOKIE_SESSION, false, [], {
          role: body.user.role,
          csrf: body.csrf,
          remember,
        });
        return;
      }
      const sessionToken = body.session_token as string;
      const sites = await api(new ApiClient(sessionToken)).sites();
      onAuthorized(sessionToken, remember, sites);
    } catch (err) {
      if (err instanceof ApiError) {
        setError(
          err.message === "Unable to reach Meta Gateway" || err.message === "api.unreachable"
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
    <LoginShell
      rootRef={loginRef}
      className={transitionPhase === "sealing" ? "is-leaving" : undefined}
      atmosphereStyle={
        bgUrl ? { backgroundImage: "url(" + JSON.stringify(bgUrl) + ")" } : undefined
      }
      effects={
        <KatanaCanvas
          charging={isFocused || pending || transitioning}
          chargeProgress={pending || transitioning ? 1 : 0.65}
          motionTarget={loginRef}
        />
      }
      edition={t("shell.workspace")}
      title={
        <>
          {t("login.titleFirst")}
          <br />
          <span>{t("login.titleSecond")}</span>
        </>
      }
      description={t("login.description")}
      footerRight={t("login.foundation")}
      tools={
        <>
          <IconButton
            label={t(scheme === "dark" ? "app.themeLight" : "app.themeDark")}
            onClick={toggleScheme}
          >
            {scheme === "dark" ? <Sun size={17} /> : <Moon size={17} />}
          </IconButton>
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
                <div className="bg-picker" style={{ top: bgPos.top, left: bgPos.left }}>
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
                      onClick={() => bgInputRef.current && applyBg(bgInputRef.current.value)}
                    >
                      {t("app.connect.bgApply")}
                    </button>
                    <label className="bg-upload-btn">
                      <input type="file" accept="image/*" hidden onChange={onBgFile} />
                      {t("app.connect.bgUpload")}
                    </label>
                    <button onClick={() => applyBg("")}>{t("app.connect.bgClear")}</button>
                  </div>
                </div>,
                document.body,
              )
            : null}
        </>
      }
    >
      <div className="login-card-stack" data-layer={accepting || otherOpen ? "other" : "signin"}>
        {accepting ? (
          <section
            className={"login-card" + (isFocused ? " is-focused" : "")}
            aria-labelledby="login-card-title"
          >
            <div className="login-card-heading">
              <span className="login-card-mark">
                <ShieldCheck size={22} strokeWidth={1.4} />
              </span>
              <h2 id="login-card-title">{team(recoveryToken ? "recover" : "accept")}</h2>
              <p>{team("loginHint")}</p>
            </div>
            <AcceptFlow
              invite={inviteToken}
              recovery={recoveryToken}
              manual={manualCode}
              t={team}
              onDone={connectMember}
              onBack={() => {
                setManualCode(false);
                history.replaceState(null, "", "/console");
              }}
            />
            <p className="login-private">
              <ShieldCheck size={13} />
              {t("login.private")}
            </p>
          </section>
        ) : otherOpen ? (
          <section className="login-card is-other" aria-labelledby="login-card-title">
            <div className="login-card-heading">
              <span className="login-card-mark">
                <ShieldCheck size={22} strokeWidth={1.4} />
              </span>
              <h2 id="login-card-title">{t("login.otherTitle")}</h2>
              <p>{t("login.otherHint")}</p>
            </div>
            <div className="login-other-list">
              {/* Registration is the same flow as arriving through an invite link:
						    only where the code came from differs. */}
              <button
                type="button"
                className="login-other-item"
                disabled={pending || transitioning}
                onClick={() => setManualCode(true)}
              >
                <Ticket size={16} />
                <span>{t("login.register")}</span>
                <ChevronRight size={15} />
              </button>
              {providers.map((provider) => (
                <a
                  key={provider.id}
                  className="login-other-item"
                  href={"/auth/oauth/" + provider.id + "/start"}
                >
                  <LogIn size={16} />
                  <span>{t("login.viaProvider", { provider: provider.label })}</span>
                  <ChevronRight size={15} />
                </a>
              ))}
              <button
                type="button"
                className="login-other-item"
                disabled={pending || transitioning}
                onClick={() => setUpgradeHelp(true)}
              >
                <HelpCircle size={16} />
                <span>{t("login.upgradeHelp")}</span>
                <ChevronRight size={15} />
              </button>
            </div>
            <button
              type="button"
              className="login-back"
              disabled={pending || transitioning}
              onClick={() => setOtherOpen(false)}
            >
              <ArrowLeft size={14} />
              {t("login.backToSignIn")}
            </button>
            <p className="login-private">
              <ShieldCheck size={13} />
              {t("login.private")}
            </p>
          </section>
        ) : (
          <>
            <section
              className={"login-card" + (isFocused ? " is-focused" : "")}
              aria-labelledby="login-card-title"
            >
              <div className="login-card-heading">
                <span className="login-card-mark">
                  <ShieldCheck size={22} strokeWidth={1.4} />
                </span>
                <h2 id="login-card-title">{t("login.welcome")}</h2>
                <p>{t("login.hint")}</p>
              </div>
              <form onSubmit={submit} aria-busy={pending || transitioning}>
                <Field label={t("app.connect.username")}>
                  <input
                    autoFocus
                    type="text"
                    value={username}
                    onChange={(e) => setUsername(e.target.value)}
                    onFocus={() => setIsFocused(true)}
                    onBlur={() => setIsFocused(false)}
                    autoComplete="username"
                    disabled={pending || transitioning}
                    required
                  />
                </Field>
                <Field label={t("app.connect.password")}>
                  <input
                    type="password"
                    value={password}
                    onChange={(e) => setPassword(e.target.value)}
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
                {error && (
                  <div className="inline-error" role="alert">
                    {error}
                  </div>
                )}
                <Button
                  type="submit"
                  disabled={pending || transitioning || !username.trim() || !password}
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
              <p className="login-private">
                <ShieldCheck size={13} />
                {t("login.private")}
              </p>
            </section>
            {/* The layer behind: its exposed edge is the whole affordance, and
					    clicking it turns the card over. Registration, third-party sign-in
					    and the upgrade guide live back there instead of competing with the
					    form as a row of links. */}
            <button
              type="button"
              className="login-card-peek"
              disabled={pending || transitioning}
              onClick={() => setOtherOpen(true)}
            >
              <span>{t("login.moreWays")}</span>
              <ChevronRight size={14} />
            </button>
          </>
        )}
      </div>
      {upgradeHelp ? (
        <Dialog title={t("login.upgradeHelp")} onClose={() => setUpgradeHelp(false)}>
          <p>{t("login.upgradeCredentials")}</p>
          <p>{t("login.upgradeCollision")}</p>
          <p>{t("login.upgradeTeam")}</p>
          <p>{t("operator.legacyHint")}</p>
          <form onSubmit={(event) => void submit(event, true)}>
            <Field label={t("operator.confirmToken")}>
              <input
                type="password"
                autoComplete="current-password"
                value={password}
                onChange={(event) => setPassword(event.target.value)}
                required
                disabled={pending || transitioning}
              />
            </Field>
            {needTOTP ? (
              <Field label={t("app.connect.totp")}>
                <input
                  inputMode="numeric"
                  pattern="[0-9]{6}"
                  autoComplete="one-time-code"
                  value={totpCode}
                  onChange={(event) => setTotpCode(event.target.value)}
                  required
                  disabled={pending || transitioning}
                />
              </Field>
            ) : null}
            {error ? <p role="alert">{error}</p> : null}
            <Button type="submit" disabled={!password || pending || transitioning}>
              {t("operator.legacyLogin")}
            </Button>
          </form>
        </Dialog>
      ) : null}
    </LoginShell>
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
  const { client, role } = useSession();
  const { t } = useI18n();
  // The gateway's site list is staff-only, and the session a member holds was
  // already proven by the /me call that established it — so a member skips
  // the query entirely instead of being shown its 403 as a full-page error.
  const staff = isStaff(role);
  const auth = useQuery({
    queryKey: ["auth", clientKey],
    queryFn: ({ signal }) => api(client!).sites(signal),
    initialData: initialSites,
    enabled: staff,
  });
  useEffect(() => {
    if (auth.error instanceof ApiError && auth.error.status === 401) onUnauthorized();
  }, [auth.error, onUnauthorized]);
  if (staff && auth.isPending)
    return (
      <div className="fullscreen-state">
        <Loading />
      </div>
    );
  if (staff && auth.isError)
    return (
      <div className="fullscreen-state">
        <ErrorState error={auth.error} retry={() => auth.refetch()} />
        <Button variant="secondary" onClick={onUnauthorized}>
          {t("app.disconnect")}
        </Button>
      </div>
    );
  return <AuthenticatedShell onUnauthorized={onUnauthorized} entranceActive={entranceActive} />;
}
/**
 * Keeps a member out of the gateway's own pages.
 *
 * The API refuses those endpoints to a member regardless (the team principal
 * gate), so this is not the security boundary — it is the difference between
 * "you cannot do that" and walking into a page that will only error. A member
 * who types one of these paths into the address bar lands back on the overview.
 */
function RouteGuard({ role, pathname }: { role: ConsoleRole; pathname: string }) {
  const navigate = useNavigate();
  const blocked =
    !isStaff(role) &&
    STAFF_ONLY_PATHS.some((path) => pathname === path || pathname.startsWith(path + "/"));
  useEffect(() => {
    if (blocked) navigate("/", { replace: true });
  }, [blocked, navigate]);
  return null;
}

function AuthenticatedShell({
  onUnauthorized,
  entranceActive,
}: {
  onUnauthorized: () => void;
  entranceActive: boolean;
}) {
  const { t } = useI18n();
  const { addons } = useModules();
  // Which chrome entries the operator keeps (Settings → Appearance: the top
  // bar's controls and the navigation rows). Display only — see lib/topBar.ts.
  const chrome = useChromePrefs();
  const operatingMode = useOperatingMode();
  // Plugin entries the operator hid from the sidebar (a display preference,
  // stored per browser like the theme).
  const hiddenPlugins = useHiddenPlugins();
  const [paletteOpen, setPaletteOpen] = useState(false);
  const [operatorPromptReady, setOperatorPromptReady] = useState(false);
  const { client, role } = useSession();
  // A raw admin token (role === null) is the operator themselves, so it ranks
  // with an owner. Everything the console shows is then filtered by role:
  // staff get the gateway's pages, a member gets the pages that are theirs.
  const effectiveRole = role ?? "owner";
  const allowedFor = (item: ChromeNavItem) => canAccessNav(item, role);
  const usersRequest = useCallback(
    <T,>(path: string, init?: RequestInit) => client!.request<T>(path, init),
    [client],
  );
  // Real telemetry: channel health drives the deck readout instead of a static
  // ONLINE. It is the gateway's own plumbing, so a member never asks for it —
  // the readout simply reports nothing rather than an error.
  const channelStats = useQuery({
    queryKey: ["channel-overviews"],
    queryFn: ({ signal }) => api(client!).channelOverviews(signal),
    enabled: isStaff(role),
    refetchInterval: 30_000,
  });
  const healthy = (channelStats.data ?? []).filter(
    (o) => channelHealthState(o) === "healthy",
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
    // Releasing the gateway's image is an operator's decision.
    enabled: role === null || role === "owner",
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
    if (location.pathname === "/setup" || location.pathname === "/team") return;
    if (
      location.pathname === "/settings" &&
      ["mode", "runtime"].includes(new URLSearchParams(location.search).get("tab") ?? "")
    )
      return;
    if (!channelStats.isSuccess) return;
    if ((channelStats.data?.length ?? 0) !== 0) return;
    if (window.localStorage.getItem("mg.setup-wizard.done") === "1") return;
    navigate("/setup", { replace: true });
  }, [channelStats.isSuccess, channelStats.data, location.pathname, location.search, navigate]);

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

  // The navigation is built from the shared catalogue (lib/chromeNav.ts), which
  // the appearance panel switches entry by entry. Plugin rows are appended from
  // runtime data and keep their own per-plugin hide switch.
  const enabledPlugins = addons
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
    .map((m) => ({ to: m.open_path!, label: m.name, icon: Puzzle }));

  // The multi-user area is gated by the operating mode (see lib/topBar.ts):
  // absence is the default on a personal gateway, and pinning the entry in the
  // appearance panel keeps it for an operator who wants the door early.
  const modeHidden = new Set(modeHiddenNav(operatingMode.data?.mode));
  const pinnedNav = new Set(chrome.pinnedNav);
  const modeHidesNav = (path: string) => modeHidden.has(path) && !pinnedNav.has(path);

  const mainNav = [
    ...CHROME_NAV_ITEMS.filter(
      (item) => item.group === "primary" && !modeHidesNav(item.path) && allowedFor(item),
    ).map((item) => ({
      to: item.path,
      label: t(item.labelKey),
      icon: item.icon,
    })),
    ...enabledPlugins,
  ];
  const settingsNav = (() => {
    const item = CHROME_NAV_ITEMS.find((entry) => entry.group === "settings");
    // A member has no settings page to open; their own account page is in
    // the main entries (/account), which is where their preferences live.
    if (!item || !allowedFor(item)) return null;
    return { to: item.path, label: t(item.labelKey), icon: item.icon };
  })();

  // The command palette lists every page, hidden entries included: hiding an
  // entry is a display choice, never a way to make a page unreachable.
  const paletteNav = [...mainNav, ...(settingsNav ? [settingsNav] : [])];

  // What the chrome renders: the same entries minus the ones switched off in
  // Settings → Appearance. Routes stay mounted either way, so a hidden page is
  // still one ⌘K away — and one click from the panel.
  const hidden = new Set(chrome.hiddenNav);
  const primaryNav = mainNav.filter((item) => !hidden.has(item.to));

  const corePaths = ["/", "/channels", "/models", "/keys"];
  const activityPaths = ["/workbench", "/logs", "/checkins"];
  const navSections = [
    {
      label: t("shell.section.gateway"),
      items: primaryNav.filter((item) => corePaths.includes(item.to)),
    },
    {
      label: t("shell.section.activity"),
      items: primaryNav.filter((item) => activityPaths.includes(item.to)),
    },
    {
      label: t("shell.section.manage"),
      items: [
        ...primaryNav.filter(
          (item) => !corePaths.includes(item.to) && !activityPaths.includes(item.to),
        ),
        ...(settingsNav && !hidden.has(settingsNav.to) ? [settingsNav] : []),
      ],
    },
  ];
  return (
    <>
      <OperatorUpgradePrompt onReady={setOperatorPromptReady} />
      <ConsoleShell
        appearance={appearance}
        sections={navSections}
        version={gatewayVersion}
        theme={theme}
        onThemeChange={changeTheme}
        onSearch={() => setPaletteOpen(true)}
        onDisconnect={onUnauthorized}
        health={{ healthy, total, loading: channelStats.isPending, available: isStaff(role) }}
        update={
          (role === null || role === "owner") && updateCheck.data?.has_update
            ? updateCheck.data
            : undefined
        }
        background={consoleBg}
        entering={Boolean(routeAnim)}
      >
        <RouteGuard role={effectiveRole} pathname={location.pathname} />
        <Suspense fallback={<Loading />}>
          <Routes>
            <Route index element={<Dashboard />} />
            <Route path="setup" element={<SetupWizard />} />
            <Route path="channels" element={<Channels />} />
            <Route path="models/channel/:channelId" element={<ChannelModels />} />
            <Route path="models" element={<Models />} />
            <Route path="keys" element={<Keys />} />
            {/* A member's own account: credit, request preferences, the
						    member app's former settings page, at the member's own path. */}
            <Route
              path="account"
              element={role === null ? <Navigate to="/settings" replace /> : <AccountPage />}
            />
            <Route path="users" element={<UsersLayout request={usersRequest} />}>
              <Route index element={<Navigate to="overview" replace />} />
              <Route path="overview" element={<UsersBoards.OverviewPanel />} />
              <Route path="members" element={<UsersBoards.MembersPanel />} />
              <Route path="policies" element={<UsersBoards.PoliciesPanel />} />
              <Route path="quotas" element={<UsersBoards.QuotasPanel />} />
              <Route path="pricing" element={<UsersBoards.PricingPanel />} />
              <Route path="codes" element={<UsersBoards.CodesPanel />} />
              <Route path="oauth" element={<UsersBoards.OAuthRoutePanel />} />
              <Route path="branding" element={<UsersBoards.BrandingPanel />} />
            </Route>
            {/* /team was this module's first path. It stays a redirect so old links
						    land on the module instead of the dashboard. */}
            <Route path="team" element={<Navigate to="/users" replace />} />
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
            <Route path="operations" element={<Navigate to="/logs?tab=discovery" replace />} />
            <Route path="assets" element={<Navigate to="/" replace />} />
            <Route path="dashboard" element={<Navigate to="/" replace />} />
            <Route path="*" element={<Navigate to="/" replace />} />
          </Routes>
        </Suspense>
      </ConsoleShell>
      <CommandPalette open={paletteOpen} onClose={() => setPaletteOpen(false)} nav={paletteNav} />
      <GuidedTour enabled={!entranceActive && isStaff(role) && operatorPromptReady} />
    </>
  );
}
