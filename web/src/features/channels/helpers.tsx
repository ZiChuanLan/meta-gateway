import type { ChannelOverview } from "../../api/types";
import { type SelectOption } from "../../components/SearchableSelect";
import { CONNECTION_TYPE_OPTIONS } from "../../connectionTypes";
import { channelReadiness } from "../channelHealth";

export const TYPE_OPTIONS: SelectOption[] = [
  ...CONNECTION_TYPE_OPTIONS.map((option) => ({
    value: option.value,
    label: option.label,
    group: option.group,
  })),
  { value: "__custom__", label: "Custom…", group: "other" },
];

export const TYPE_GROUPS = ["core", "relay", "other"];

/**
 * Which user-account credential fields a channel type can make use of.
 * "both" = 用户 Access Token + 用户 Cookie (New-API-family account surfaces),
 * "cookie" = cookie-only (generic external check-in sites), "none" = the type
 * has no user-account system (plain OpenAI-compatible relays, official
 * provider APIs, unsupported families) — the fields would be dead weight.
 */
export type UserAuthFields = "both" | "cookie" | "none";

const COOKIE_ONLY_USER_AUTH_TYPES = new Set(["external-checkin"]);

const NO_USER_AUTH_TYPES = new Set([
  // Plain OpenAI-compatible relays and official provider APIs.
  "openai-compatible",
  "anthropic",
  "gemini",
  "deepseek",
  "moonshot",
  "zhipu",
  "qwen",
  "doubao",
  "siliconflow",
  "minimax",
  "stepfun",
  "lingyiwanwu",
  "baichuan",
  "spark",
  "hunyuan",
  "qianfan",
  "openrouter",
  "groq",
  "xai",
  "mistral",
  "perplexity",
  // Site families with no server-side account adapter.
  "octopus",
  "axonhub",
  "claude-code-hub",
]);

export function userAuthFieldsFor(typeHint: string): UserAuthFields {
  const key = (typeHint || "").trim().toLowerCase();
  if (!key) return "both";
  if (COOKIE_ONLY_USER_AUTH_TYPES.has(key)) return "cookie";
  if (NO_USER_AUTH_TYPES.has(key)) return "none";
  // New-API-family brands and unknown/custom brands: the backend defaults
  // unknown families to the New-API profile, so keep the fields available.
  return "both";
}

export /** Value shown in secret inputs when a credential is stored; keeping it means "don't change". */
const SECRET_MASK = "••••••••••";

export type ConnectionHealthFilter =
  "all" | "ready" | "attention" | "missing_key";

export function isMissingAPIKey(overview: ChannelOverview) {
  return !overview.has_api_key;
}

export type CreateConnectionInput = {
  name: string;
  base_url: string;
  secret: string;
  type_hint: string;
  group_name?: string;
  /** Empty/undefined = inherit the Admin-configured default. */
  model_sync_mode?: "auto" | "manual";
};

export function normalizeBase(url: string) {
  return url.trim().replace(/\/+$/, "");
}

export function needsVerify(overview: ChannelOverview) {
  return (
    channelReadiness(overview) === "unverified" || overview.model_count === 0
  );
}

export function capabilityFlags(overview: ChannelOverview) {
  const hasUser = Boolean(overview.has_user_credential);
  const hasPlatformUserID = Boolean(overview.has_platform_user_id);
  const hasAPIKey = Boolean(overview.has_api_key);
  const modelsReady = overview.model_count > 0;
  const checkinScheduled = Boolean(overview.checkin_enabled);
  // Site-family capabilities (AAH-derived profile): sub2api/aihubmix/
  // sharedchat do not support check-in; unsupported families have no
  // account APIs at all.
  const checkinSupported = Boolean(overview.checkin_supported);
  const accountSupported = Boolean(overview.account_supported);
  // New-API family check-in needs user token + numeric user id (may be filled on first run).
  const checkinReady = hasUser && hasPlatformUserID && checkinSupported;
  const checkinNeedsUserID = hasUser && !hasPlatformUserID && checkinSupported;
  return {
    hasUser,
    hasPlatformUserID,
    hasAPIKey,
    checkinSupported,
    accountSupported,
    /** Token + user id present; manual check-in is fully prepared. */
    checkinCapable: checkinReady,
    checkinReady,
    checkinNeedsUserID,
    /** Scheduled check-in is enabled on a user credential. */
    checkinScheduled: checkinScheduled && checkinReady,
    /** User token exists but schedule is off. */
    checkinOff: checkinReady && !checkinScheduled,
    /** No user token at all — cannot check in. */
    noUserToken: !hasUser,
    missingAPIKey: !hasAPIKey,
    /** An access token is stored, was checked, and that check failed — the token itself is the problem. */
    tokenProblem:
      hasUser &&
      Boolean(overview.last_account_probe_at) &&
      overview.last_account_probe_ok === false,
    modelsReady,
    needsKeyForRelay: !hasAPIKey,
  };
}
