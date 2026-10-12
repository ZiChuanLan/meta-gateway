import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { createBrowserRouter, RouterProvider } from "react-router-dom";
import { I18nProvider } from "./i18n";
import { SessionProvider } from "./session";
import { ToastProvider } from "./toast";
import { App } from "./App";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { installPreloadRecovery } from "./lib/preloadRecovery";
import { UnsavedChangesBoundary } from "./lib/RouterBoundary";
import "./styles.css";
import "./themes/modern/theme.css";
import "./themes/classic/theme.css";
import "./themes/classic/compat.css";
import "./styles/theme-gallery.css";
// Last on purpose: a palette ties on specificity with the theme token blocks,
// so cascade order is what lets it win. See styles/palettes.css.
import "./styles/palettes.css";
import "./styles/availability.css";
import "./styles/billing.css";
import "./styles/exchange-workspace.css";
// The member views' own styles: the sheets the member app used to carry, kept
// only for the few controls that are genuinely a member's own (the credit
// block, the arrangement editor's handle/rank columns, the redeem box). The
// member *shell* that used to live here was deleted with the pages it dressed —
// members now render inside the console's own shell and panels, so a second
// copy of a top bar, side rail or card could only drift from the real one.
import "./member/workspaces.css";

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 15_000, retry: false, refetchOnWindowFocus: false },
    mutations: { retry: false },
  },
});

// A tab opened before a deploy runs the old build while the server only has the
// new chunks; reloading once picks up the shell that matches them.
installPreloadRecovery();

// A data router, not `BrowserRouter`: it is what lets the unsaved-changes guard
// hold back a navigation the app did not start (the browser's Back/Forward
// buttons). The single splat route keeps App's own <Routes> in charge of the
// page tree, so no page had to change — see lib/RouterBoundary.tsx.
const router = createBrowserRouter(
  [
    {
      path: "*",
      element: (
        <UnsavedChangesBoundary>
          <ErrorBoundary>
            <App />
          </ErrorBoundary>
        </UnsavedChangesBoundary>
      ),
    },
  ],
  { basename: "/console" },
);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <QueryClientProvider client={queryClient}>
      <I18nProvider>
        <ToastProvider>
          <SessionProvider>
            <RouterProvider router={router} />
          </SessionProvider>
        </ToastProvider>
      </I18nProvider>
    </QueryClientProvider>
  </StrictMode>,
);
