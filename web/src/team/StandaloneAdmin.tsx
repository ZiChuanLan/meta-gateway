import { Suspense } from "react";
import { MemoryRouter, Navigate, Route, Routes } from "react-router-dom";
import { Loading } from "../components/ui";
import { UsersLayout } from "./UsersLayout";
import { MembersPanel, OverviewPanel } from "./panels/lazy";
import type { TeamRequest } from "./types";

/**
 * The screen a team admin (a member whose role is `admin`, not `owner`) sees
 * when they sign in to the console.
 *
 * It is the multi-user module on its own, without the gateway's console around
 * it: an admin administers people, not upstream channels, and the console's
 * other pages are the owner's business. The routes are held in memory because
 * this screen owns the whole document — there is no console router above it to
 * hang paths off, and an admin has no reason to bookmark a board inside a
 * sign-in session.
 *
 * Only the two boards an admin may actually open are mounted here; UsersLayout
 * hides the owner-only sections for the same reason.
 */
export function StandaloneAdmin({ request }: { request: TeamRequest }) {
  return (
    <MemoryRouter initialEntries={["/overview"]}>
      <Suspense fallback={<Loading />}>
        <Routes>
          <Route path="/" element={<UsersLayout request={request} />}>
            <Route index element={<Navigate to="overview" replace />} />
            <Route path="overview" element={<OverviewPanel />} />
            <Route path="members" element={<MembersPanel />} />
          </Route>
        </Routes>
      </Suspense>
    </MemoryRouter>
  );
}
