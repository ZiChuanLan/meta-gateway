import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { SessionProvider } from "../session";
import { DeploymentStepPrompt } from "./DeploymentStepPrompt";
import type { ReactNode } from "react";

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
  localStorage.clear();
  sessionStorage.clear();
});

function mount(children: ReactNode) {
  localStorage.setItem("meta-gateway.locale", "en");
  localStorage.setItem("meta-gateway.admin-token", "session-token");
  return render(
    <QueryClientProvider
      client={
        new QueryClient({
          defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
        })
      }
    >
      <I18nProvider>
        <SessionProvider>{children}</SessionProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
}

function stubSelfUpdate(payload: Record<string, unknown>) {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (input: RequestInfo | URL) => {
      if (String(input).includes("/admin/self-update")) {
        return new Response(
          JSON.stringify({ available: true, running: false, phase: "idle", ...payload }),
        );
      }
      return new Response(JSON.stringify({}));
    }),
  );
}

// An upgrade through the console replaces the image but never re-reads the
// deployment file, so the container keeps its old environment. Nothing in the
// running gateway would otherwise say so, and the operator's `.env` edits stay
// inert — that is what this prompt is for.
it("shows the one-time command when the container predates the deployment file", async () => {
  stubSelfUpdate({ mode: "watchtower", deployment_step: "compose_recreate" });
  mount(<DeploymentStepPrompt enabled />);
  expect(await screen.findByText("One step left: apply the deployment file")).toBeTruthy();
  // The command has to be the real one, in one copyable line.
  expect(
    screen.getByText(
      "git pull --ff-only && docker compose pull meta-gateway && docker compose up -d --no-build --force-recreate meta-gateway",
    ),
  ).toBeTruthy();
});

// The server decides this from the container's own environment, so a deployment
// that already applied the file shows nothing — no dismissal to clean up.
it("stays quiet when the environment is already aligned", async () => {
  stubSelfUpdate({ mode: "compose", deployment_step: undefined });
  mount(<DeploymentStepPrompt enabled />);
  await waitFor(() =>
    expect(screen.queryByText("One step left: apply the deployment file")).toBeNull(),
  );
});

// "Later" silences it for this browser session only: the step is still undone, so
// a new session has to say so again.
it("can be silenced for the session", async () => {
  stubSelfUpdate({ deployment_step: "compose_recreate" });
  mount(<DeploymentStepPrompt enabled />);
  fireEvent.click(await screen.findByText("Later"));
  await waitFor(() =>
    expect(screen.queryByText("One step left: apply the deployment file")).toBeNull(),
  );
  expect(sessionStorage.getItem("deployment-step-dismissed")).toBe("1");
});

// The claim prompt comes first; this one must not stack on top of it.
it("waits until the earlier prompt is resolved", async () => {
  stubSelfUpdate({ deployment_step: "compose_recreate" });
  mount(<DeploymentStepPrompt enabled={false} />);
  await waitFor(() =>
    expect(screen.queryByText("One step left: apply the deployment file")).toBeNull(),
  );
});
