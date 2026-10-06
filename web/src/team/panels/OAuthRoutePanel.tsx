import { useQuery } from "@tanstack/react-query";
import { OAuthPanel } from "../OAuthPanel";
import { useUsers } from "../UsersContext";
import type { Policy } from "../types";

/**
 * Third-party sign-in for members (GitHub / Linux.do).
 *
 * The board is OAuthPanel; this route exists so the module's navigation can
 * address it like any other board, and so it can fetch the policy list its
 * registration form needs without the shell having to pass it down.
 */
export function OAuthRoutePanel() {
  const { request, locale, t } = useUsers();
  const policies = useQuery({
    queryKey: ["team", "policies"],
    queryFn: ({ signal }) => request<Policy[]>("/admin/team/policies", { signal }),
  });
  return (
    <OAuthPanel
      request={request}
      policies={policies.data ?? []}
      locale={locale}
      t={t}
    />
  );
}
