import { Suspense } from "react";
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
export function StandaloneAdmin({ request }: { request: TeamRequest }) {
  return (
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
  );
}
