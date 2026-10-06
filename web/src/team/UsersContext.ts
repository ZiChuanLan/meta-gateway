import { useOutletContext } from "react-router-dom";
import type { TeamText } from "./text";
import type { TeamRequest, TeamSettings } from "./types";

/** Shared board contract, independent of the layout's lazy-loaded module. */
export interface UsersContext {
  request: TeamRequest;
  locale: string;
  t: TeamText;
  owner: boolean;
  settings: TeamSettings;
  hasOwner: boolean;
}

export function useUsers(): UsersContext {
  return useOutletContext<UsersContext>();
}
