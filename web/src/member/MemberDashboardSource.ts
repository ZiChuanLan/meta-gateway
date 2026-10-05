import type { ModelUsage, UsageSeries, UsageSummary } from "../api/types";
import type { DashboardSource } from "../features/Dashboard";
import { accountRequest } from "../team/transport";
import { memberLogsSource } from "./MemberLogsSource";

/**
 * The member app's overview data.
 *
 * Every path here is /me/*: the account comes from the session, never from a
 * parameter, so this source cannot ask for anyone else's numbers even by
 * accident. The series and ranking endpoints are the console's own aggregates
 * scoped by account — which is what makes a member's figures add up to their
 * bill rather than to one key's share of it.
 */
export const memberDashboardSource: DashboardSource = {
  summary: (query, signal) =>
    accountRequest<UsageSummary>(PATH_SUMMARY + windowQuery(query), { signal }),
  series: (query, signal) =>
    accountRequest<UsageSeries>(PATH_SERIES + windowQuery(query), { signal }),
  topModels: (query, signal) =>
    accountRequest<ModelUsage[]>(PATH_TOP_MODELS + windowQuery(query), { signal }),
  recent: (query, signal) =>
    // Reuse the log page's mapping: there is one definition of what a member's
    // request row looks like, and this page is not a second one.
    memberLogsSource.logs({ since: query.since, until: query.until }, signal),
};

const PATH_SUMMARY = "/me/usage/summary";
const PATH_SERIES = "/me/usage/series";
const PATH_TOP_MODELS = "/me/usage/top-models";

/** Builds the ?since/until/buckets/limit suffix from whatever was supplied. */
function windowQuery(values: {
  since?: string;
  until?: string;
  buckets?: number;
  limit?: number;
}): string {
  const params = new URLSearchParams();
  if (values.since) params.set("since", values.since);
  if (values.until) params.set("until", values.until);
  if (values.buckets != null) params.set("buckets", String(values.buckets));
  if (values.limit != null) params.set("limit", String(values.limit));
  const query = params.toString();
  return query ? "?" + query : "";
}
