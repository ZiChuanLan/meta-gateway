import { lazy } from "react";

/**
 * The module's boards, loaded on demand.
 *
 * They are addressed as nested routes, so each one has to name a component in
 * the route table; keeping those `lazy()` calls here means the route table
 * stays a list of boards instead of a list of import paths, and the whole
 * multi-user module stays out of the console's first paint — it is a page most
 * gateways never open.
 *
 * Named exports are mapped to `default` because that is what `lazy` wants.
 */
export const OverviewPanel = lazy(() =>
  import("./OverviewPanel").then((m) => ({ default: m.OverviewPanel })),
);
export const UsersLayout = lazy(() =>
  import("../UsersLayout").then((module) => ({ default: module.UsersLayout })),
);
export const MembersPanel = lazy(() =>
  import("./MembersPanel").then((m) => ({ default: m.MembersPanel })),
);
export const PoliciesPanel = lazy(() =>
  import("./PoliciesPanel").then((m) => ({ default: m.PoliciesPanel })),
);
export const QuotasPanel = lazy(() =>
  import("./QuotasPanel").then((m) => ({ default: m.QuotasPanel })),
);
export const PricingPanel = lazy(() =>
  import("./PricingPanel").then((m) => ({ default: m.PricingPanel })),
);
export const CodesPanel = lazy(() =>
  import("./CodesPanel").then((m) => ({ default: m.CodesPanel })),
);
export const OAuthRoutePanel = lazy(() =>
  import("./OAuthRoutePanel").then((m) => ({ default: m.OAuthRoutePanel })),
);
export const BrandingPanel = lazy(() =>
  import("./BrandingPanel").then((m) => ({ default: m.BrandingPanel })),
);
