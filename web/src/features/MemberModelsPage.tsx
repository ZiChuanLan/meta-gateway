import { useCallback, useState } from "react";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { Loading, ErrorState } from "../components/ui";
import { useI18n } from "../i18n";
import { useToast } from "../toast";
import { accountRequest } from "../team/transport";
import { teamText, teamError } from "../team/text";
import type { Account, Plan, UserKey } from "../team/types";
import { ConnectDialog } from "../member/ConnectDialog";
import { ModelsWorkspace } from "../member/ModelsWorkspace";

/**
 * The member's model page, inside the console.
 *
 * The page body is the member app's own (`ModelsWorkspace`): the catalogue they
 * may call, their arrangement editor and the connect hand-off. What this
 * wrapper adds is the data the workspace expects its host to have fetched —
 * account, plans, authorized models, keys — because in the member shell that
 * host was `UserApp`, and the console is not going to grow a second copy of the
 * member shell to satisfy a prop list.
 *
 * The arrangement choice is remembered under the member app's own key, so a
 * plan selected before the move is still selected after it.
 */
const PLAN_KEY = "meta-gateway.team.plan";

export function MemberModelsPage() {
  const { locale } = useI18n();
  const t = teamText(locale);
  const { push } = useToast();
  const qc = useQueryClient();
  const [planId, setPlanId] = useState(() => {
    try {
      return Number(localStorage.getItem(PLAN_KEY) ?? 0) || 0;
    } catch {
      return 0;
    }
  });
  const [busy, setBusy] = useState(false);
  const [connecting, setConnecting] = useState<{
    key: UserKey;
    token?: string;
    model?: string;
  } | null>(null);

  const account = useQuery({
    queryKey: ["me", "account"],
    queryFn: ({ signal }) => accountRequest<Account>("/me", { signal }),
  });
  const userID = account.data?.user.id;
  const canRoute = Boolean(
    account.data?.policy.allow_routing && account.data?.branding.show_routing,
  );
  const plans = useQuery({
    queryKey: ["user", userID, "plans"],
    queryFn: ({ signal }) => accountRequest<Plan[]>("/me/plans", { signal }),
    enabled: Boolean(userID) && canRoute,
  });
  const models = useQuery({
    queryKey: ["user", userID, "models"],
    queryFn: ({ signal }) => accountRequest<string[]>("/me/models", { signal }),
    enabled: Boolean(userID),
  });
  const keys = useQuery({
    queryKey: ["user", userID, "keys"],
    queryFn: ({ signal }) => accountRequest<UserKey[]>("/me/keys", { signal }),
    enabled: Boolean(userID),
  });

  const selectPlan = useCallback((id: number) => {
    setPlanId(id);
    try {
      localStorage.setItem(PLAN_KEY, String(id));
    } catch {
      /* optional preference */
    }
  }, []);
  const createPlan = useCallback(async () => {
    const name = window.prompt(t("planName"));
    if (!name?.trim()) return;
    setBusy(true);
    try {
      const created = await accountRequest<Plan>("/me/plans", {
        method: "POST",
        body: JSON.stringify({ name: name.trim() }),
      });
      await qc.invalidateQueries({ queryKey: ["user", userID, "plans"] });
      if (created?.id) selectPlan(created.id);
    } catch (error) {
      push({ message: teamError(error, locale), tone: "error" });
    } finally {
      setBusy(false);
    }
  }, [qc, selectPlan, t, userID, push, locale]);

  if (account.isPending) return <Loading />;
  if (!account.data) return <ErrorState error={account.error} retry={() => void account.refetch()} />;
  const branding = account.data.branding;
  const base = branding.api_base_url || `${location.origin}/v1`;
  return (
    <>
      <ModelsWorkspace
        userID={account.data.user.id}
        locale={locale}
        canRoute={canRoute}
        plans={plans.data ?? []}
        planId={planId}
        onPlanChange={selectPlan}
        onCreatePlan={() => void createPlan()}
        onConnect={(model) => {
          // Connect needs a live token; the first enabled one is the obvious
          // choice, and a member with none is told to make one on the keys page.
          const enabled = keys.data?.find((key) => key.enabled);
          if (enabled) setConnecting({ key: enabled, model });
          else push({ message: t("noKeysYet"), tone: "info" });
        }}
      />
      {connecting ? (
        <ConnectDialog
          apiKey={connecting.key}
          token={connecting.token}
          initialModel={connecting.model}
          base={base}
          brandName={branding.name || "Meta Gateway"}
          models={models.data ?? []}
          t={t}
          onClose={() => setConnecting(null)}
          onCopy={(value, message) => {
            void navigator.clipboard.writeText(value);
            push({ message: message || t("copied") });
          }}
        />
      ) : null}
      {busy ? <Loading /> : null}
    </>
  );
}
