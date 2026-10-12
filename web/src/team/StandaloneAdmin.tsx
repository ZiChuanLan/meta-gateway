import { Suspense, useEffect, useState } from "react";
import { Users } from "lucide-react";
import { ConsoleShell } from "../components/ConsoleShell";
import { CommandPalette } from "../components/CommandPalette";
import { useAppearance } from "../appearance";
import { useI18n } from "../i18n";
import { Navigate, Route, Routes } from "react-router-dom";
import { Loading } from "../components/ui";
import { UsersLayout, MembersPanel, OverviewPanel } from "./panels/lazy";
import type { TeamRequest } from "./types";

/**
 * The screen a team admin (a member whose role is `admin`, not `owner`) sees
 * when they sign in to the console.
 *
 * It is the multi-user module on its own, without the gateway's console around
 * it: an admin administers people, not upstream channels, and the console's
 * other pages are the owner's business. It uses the console's existing router:
 * a second MemoryRouter inside BrowserRouter would crash the signed-in app.
 *
 * Only the two boards an admin may actually open are mounted here; UsersLayout
 * hides the owner-only sections for the same reason.
 */
export function StandaloneAdmin({
  request,
  onDisconnect,
}: {
  request: TeamRequest;
  onDisconnect: () => void;
}) {
  const { t } = useI18n();
  const { appearance, scheme, toggleScheme } = useAppearance();
  const [search, setSearch] = useState(false);
  const nav = [{ to: "/users", label: t("app.nav.team"), icon: Users }];
  useEffect(() => {
    const handle = (event: KeyboardEvent) => {
      if ((event.ctrlKey || event.metaKey) && event.key.toLowerCase() === "k") {
        event.preventDefault();
        setSearch((value) => !value);
      }
    };
    window.addEventListener("keydown", handle);
    return () => window.removeEventListener("keydown", handle);
  }, []);
  return (
    <ConsoleShell
      appearance={appearance}
      sections={[{ label: t("shell.section.manage"), items: nav }]}
      version=""
      theme={scheme}
      onThemeChange={toggleScheme}
      onSearch={() => setSearch(true)}
      onDisconnect={onDisconnect}
      health={{ healthy: 0, total: 0, loading: false, available: false }}
    >
      <CommandPalette open={search} onClose={() => setSearch(false)} nav={nav} />
      <Suspense fallback={<Loading />}>
        <Routes>
          <Route path="/users" element={<UsersLayout request={request} />}>
            <Route index element={<Navigate to="overview" replace />} />
            <Route path="overview" element={<OverviewPanel />} />
            <Route path="members" element={<MembersPanel />} />
          </Route>
          <Route path="*" element={<Navigate to="/users/overview" replace />} />
        </Routes>
      </Suspense>
    </ConsoleShell>
  );
}
