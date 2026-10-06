import { useQuery } from "@tanstack/react-query";
import { CodePanel } from "../CodePanel";
import { useUsers } from "../UsersContext";
import type { Policy } from "../types";

/**
 * Invitations and credit vouchers.
 *
 * Codes belong to the multi-user module, not to the client-token board they
 * used to sit under: a code creates an account or tops up an account's credit
 * pool, which are things that only exist once the gateway has members. The
 * board itself is CodePanel — kept as its own component because minting,
 * listing and revoking codes is a coherent unit; this file only supplies the
 * policy list it needs for its policy picker.
 */
export function CodesPanel() {
  const { request, locale, t } = useUsers();
  const policies = useQuery({
    queryKey: ["team", "policies"],
    queryFn: ({ signal }) => request<Policy[]>("/admin/team/policies", { signal }),
  });
  return <CodePanel request={request} policies={policies.data ?? []} locale={locale} t={t} />;
}
