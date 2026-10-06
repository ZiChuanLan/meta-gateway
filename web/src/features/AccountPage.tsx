import { useCallback, useEffect, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { ErrorState, Loading } from "../components/ui";
import { useAppearance, type ColorScheme } from "../appearance";
import { useI18n } from "../i18n";
import { useSession } from "../session";
import { accountRequest } from "../team/transport";
import { teamText } from "../team/text";
import type { Account } from "../team/types";
import { SettingsPage } from "../member/SettingsPage";

/**
 * The console's account page: the member app's settings, at a console path.
 *
 * The page itself is the member app's own component — request controls, display
 * preferences, password and sessions — and this wrapper only supplies what the
 * console has that the member shell used to: the account it already fetched,
 * the appearance settings, and the console's own sign-out.
 *
 * Density is the one piece with no console counterpart: it was the member
 * shell's state, stored under its own key and applied as `data-density` on the
 * document. It is kept exactly as it was, so the value a member set before this
 * page moved still applies.
 */
const DENSITY_KEY = "meta-gateway.team.density";

export function AccountPage() {
  const { locale, t: consoleT } = useI18n();
  const t = teamText(locale);
  const { disconnect, role } = useSession();
  const { appearance, scheme, setAppearance, setScheme } = useAppearance();
  const [density, setDensity] = useState(() => {
    try {
      return localStorage.getItem(DENSITY_KEY) ?? "comfortable";
    } catch {
      return "comfortable";
    }
  });
  useEffect(() => {
    document.documentElement.dataset.density = density;
  }, [density]);
  const applyDensity = useCallback((value: string) => {
    setDensity(value);
    try {
      localStorage.setItem(DENSITY_KEY, value);
    } catch {
      /* optional preference */
    }
  }, []);

  const account = useQuery({
    queryKey: ["me", "account"],
    queryFn: ({ signal }) => accountRequest<Account>("/me", { signal }),
  });

  if (account.isPending) return <Loading />;
  if (account.isError) {
    // /me speaks for a team account; a raw admin token has none. Saying so is
    // more useful than echoing a login error the operator cannot act on — the
    // page works the moment they sign in with an owner or member account.
    if (role === null)
      return (
        <div className="page-head">
          <h1>{consoleT("app.nav.account")}</h1>
          <p className="muted">{consoleT("account.needsTeamSession")}</p>
        </div>
      );
    return <ErrorState error={account.error} retry={() => void account.refetch()} />;
  }
  if (!account.data) return null;
  return (
    <SettingsPage
      account={account.data}
      appearance={appearance}
      onAppearance={setAppearance}
      scheme={scheme}
      onScheme={(value) => setScheme(value as ColorScheme)}
      density={density}
      onDensity={applyDensity}
      t={t}
      onDisconnected={disconnect}
    />
  );
}
