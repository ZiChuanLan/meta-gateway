import { ApiError } from "../lib/apiError";
export const COOKIE_SESSION = "@team-cookie";
let csrf = "";
let sessionGeneration = 0;
export function setTeamCSRF(value: string) {
  sessionGeneration++;
  csrf = value;
}
export function teamSessionGeneration() {
  return sessionGeneration;
}
export function teamCSRF() {
  return csrf;
}
export async function accountRequest<T>(path: string, init: RequestInit = {}): Promise<T> {
  const generation = sessionGeneration;
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (init.body) headers.set("Content-Type", "application/json");
  if (init.method && !["GET", "HEAD"].includes(init.method)) headers.set("X-Meta-CSRF", csrf);
  let response: Response;
  try {
    response = await fetch(path, {
      ...init,
      headers,
      credentials: "same-origin",
    });
  } catch (error) {
    if (
      init.signal?.aborted ||
      (typeof error === "object" &&
        error !== null &&
        "name" in error &&
        error.name === "AbortError")
    )
      throw error;
    throw new ApiError(0, "network_unavailable");
  }
  // A response sent under the old account must not populate the new account's
  // cache or expire its session after login/logout has changed the generation.
  if (generation !== sessionGeneration) throw new DOMException("Session changed", "AbortError");
  if (!response.ok) {
    if (response.status === 401 && (path.split("?")[0] === "/me" || path.startsWith("/me/")))
      window.dispatchEvent(new Event("meta-team-expired"));
    let code = response.status === 404 ? "team_disabled" : `HTTP ${response.status}`;
    try {
      const body = (await response.json()) as { error?: string };
      code = body.error ?? code;
    } catch {
      /* status fallback */
    }
    if (code === "team_disabled" && generation === sessionGeneration)
      window.dispatchEvent(new Event("meta-team-disabled"));
    throw new ApiError(response.status, code);
  }
  const data = (await response.json()) as T;
  if (generation !== sessionGeneration) throw new DOMException("Session changed", "AbortError");
  return data;
}
