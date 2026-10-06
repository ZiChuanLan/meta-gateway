import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useState,
  type ReactNode,
} from "react";
import { ApiClient } from "./api/client";
import { accountRequest, COOKIE_SESSION, setTeamCSRF } from "./team/transport";
import type { Account } from "./team/types";

const ACCOUNT_CHANGE_KEY = "meta-gateway.account-change";
function notifyAccountChange() {
  try {
    localStorage.setItem(ACCOUNT_CHANGE_KEY, crypto.randomUUID());
  } catch {
    /* optional cross-tab hint */
  }
}

const SESSION_KEY = "meta-gateway.admin-token";

/**
 * Who is signed in, as far as the console's UI is concerned.
 *
 * `owner` and `admin` are staff: they may open the gateway's own pages.
 * `member` is an ordinary account — it reaches the console with a team cookie
 * session exactly like the staff do, and sees only its own overview, tokens,
 * models, logs and workbench. `null` means a raw admin token, which is the
 * operator themselves.
 */
export type ConsoleRole = "owner" | "admin" | "member";

/** Staff may operate the gateway; a member may only use it. */
export function isStaff(role: ConsoleRole | null): boolean {
  return role === null || role === "owner" || role === "admin";
}

export type AccountSession = { role: ConsoleRole; csrf: string; remember?: boolean };

interface SessionValue {
  role: ConsoleRole | null;
  token: string | null;
  client: ApiClient | null;
  connect: (token: string, remember: boolean) => void;
  /**
   * Adopt a session that a team sign-in just established.
   *
   * The login call has already set the cookie — all this does is ask the
   * provider to read it, which is why it needs no credentials of its own. It
   * exists so the console's sign-in page can serve a member without knowing how
   * the session is discovered.
   */
  connectMember: (account?: AccountSession) => void;
  disconnect: () => void;
}

const SessionContext = createContext<SessionValue | null>(null);

function initialToken() {
  try {
    return localStorage.getItem(SESSION_KEY) ?? sessionStorage.getItem(SESSION_KEY);
  } catch {
    return null;
  }
}

function storeToken(token: string | null, remember: boolean) {
  try {
    if (token && remember) localStorage.setItem(SESSION_KEY, token);
    else {
      localStorage.removeItem(SESSION_KEY);
      sessionStorage.removeItem(SESSION_KEY);
    }
  } catch {
    // Storage can be unavailable in hardened/private browser contexts.
  }
}

export function SessionProvider({ children }: { children: ReactNode }) {
  const [token, setToken] = useState<string | null>(initialToken);
  const [role, setRole] = useState<ConsoleRole | null>(null);
  // Bumped when a member signs in through the console: the effect below already
  // knows how to adopt a team session, it just needs a reason to run again.
  const [teamAttempt, setTeamAttempt] = useState(0);
  const [requiresLogin, setRequiresLogin] = useState(false);
  const connectMember = useCallback((account?: AccountSession) => {
    notifyAccountChange();
    setRequiresLogin(false);
    try {
      localStorage.removeItem("meta-gateway.team-console");
      sessionStorage.removeItem("meta-gateway.team-console");
      (account?.remember ? localStorage : sessionStorage).setItem("meta-gateway.team-console", "1");
    } catch {
      /* a navigation hint, not a credential */
    }
    if (account) {
      storeToken(null, false);
      setTeamCSRF(account.csrf);
      setRole(account.role);
      setToken(COOKIE_SESSION);
    } else setTeamAttempt((value) => value + 1);
  }, []);
  useEffect(() => {
    if (token || requiresLogin) return;
    try {
      if (
        sessionStorage.getItem("meta-gateway.team-console") !== "1" &&
        localStorage.getItem("meta-gateway.team-console") !== "1"
      )
        return;
    } catch {
      return;
    }
    let active = true;
    accountRequest<Account>("/me")
      .then((a) => {
        if (!active) return;
        // Members reach the console too — that is the whole point of one
        // console: the same pages, scoped by role, instead of a second app.
        const role = a.user.role;
        if (role !== "owner" && role !== "admin" && role !== "member") {
          localStorage.removeItem("meta-gateway.team-console");
          sessionStorage.removeItem("meta-gateway.team-console");
          return;
        }
        setTeamCSRF(a.csrf);
        setRole(role);
        setToken(COOKIE_SESSION);
      })
      .catch((error) => {
        if (!active) return;
        // A superseded response (another request changed the session while this
        // one was in flight) says nothing about the cookie, so it must not throw
        // the operator back to the sign-in page or drop the navigation hint.
        if (error instanceof DOMException && error.name === "AbortError") return;
        try {
          localStorage.removeItem("meta-gateway.team-console");
          sessionStorage.removeItem("meta-gateway.team-console");
        } catch {
          /* optional metadata */
        }
      });
    return () => {
      active = false;
    };
  }, [token, teamAttempt, requiresLogin]);
  const connect = useCallback((next: string, remember: boolean) => {
    const trimmed = next.trim();
    storeToken(trimmed, remember);
    setRole(null);
    setToken(trimmed);
  }, []);
  const disconnect = useCallback(() => {
    if (role) notifyAccountChange();
    if (role) void accountRequest("/me/logout", { method: "POST", body: "{}" }).catch(() => {});
    try {
      sessionStorage.removeItem("meta-gateway.team-console");
      localStorage.removeItem("meta-gateway.team-console");
    } catch {
      /* optional metadata */
    }
    storeToken(null, false);
    setTeamCSRF("");
    setToken(null);
    setRole(null);
  }, [role]);
  // Expiry is a local reset, not another logout request. Avoid recursively
  // sending requests with an expired cookie. GatewayApp clears query data.
  useEffect(() => {
    if (!role) return;
    const expire = () => {
      try {
        localStorage.removeItem("meta-gateway.team-console");
        sessionStorage.removeItem("meta-gateway.team-console");
      } catch {
        /* optional navigation hints */
      }
      storeToken(null, false);
      setTeamCSRF("");
      setRole(null);
      setToken(null);
    };
    window.addEventListener("meta-team-expired", expire);
    return () => window.removeEventListener("meta-team-expired", expire);
  }, [role]);
  useEffect(() => {
    if (!role) return;
    const changed = (event: StorageEvent) => {
      if (event.key !== ACCOUNT_CHANGE_KEY || !event.newValue) return;
      // Cookie sessions are shared across tabs. Do not keep rendering account
      // A while the browser now sends account B's cookie. Never trust the hint
      // as authentication, nor send logout (it would revoke the other tab).
      setRequiresLogin(true);
      setTeamCSRF("");
      setRole(null);
      setToken(null);
      try {
        sessionStorage.removeItem("meta-gateway.team-console");
      } catch {
        /* optional */
      }
      // Do not remove shared localStorage: it belongs to the other tab's login.
    };
    window.addEventListener("storage", changed);
    return () => window.removeEventListener("storage", changed);
  }, [role]);
  const value = useMemo(
    () => ({
      role,
      token,
      client: token ? new ApiClient(token, disconnect, role ? "cookie" : "bearer") : null,
      connect,
      connectMember,
      disconnect,
    }),
    [role, token, connect, connectMember, disconnect],
  );
  return <SessionContext.Provider value={value}>{children}</SessionContext.Provider>;
}

export function useSession() {
  const value = useContext(SessionContext);
  if (!value) throw new Error("useSession must be used inside SessionProvider");
  return value;
}

/**
 * The session, or null when there is no provider — for components that only
 * USE it when one is present.
 *
 * The distinction matters for shared building blocks: the appearance settings
 * render the money controls, which need the admin client, but the appearance
 * panel itself is also mounted in contexts that have no session at all (its
 * own tests, and any future embedding). A component that requires a provider
 * cannot be reused there, while a missing session is a perfectly good reason
 * for those controls to render empty instead of crashing the page.
 */
export function useOptionalSession() {
  return useContext(SessionContext);
}
