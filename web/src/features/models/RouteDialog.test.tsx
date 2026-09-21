import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import type { Route } from "../../api/types";
import { I18nProvider } from "../../i18n";
import { SessionProvider } from "../../session";
import { RouteDialog } from "./RouteDialog";

function renderDialog(
  value: Partial<Route>,
  options: { stickyGlobalDefault?: boolean | null } = {},
) {
  const saved: Array<Record<string, unknown>> = [];
  const queryClient = new QueryClient({
    defaultOptions: { queries: { retry: false }, mutations: { retry: false } },
  });
  render(
    <QueryClientProvider client={queryClient}>
      <I18nProvider>
        <SessionProvider>
          <RouteDialog
            value={value}
            members={[]}
            pending={false}
            error={null}
            stickyGlobalDefault={options.stickyGlobalDefault}
            onClose={() => {}}
            onSave={(next) => saved.push(next as Record<string, unknown>)}
          />
        </SessionProvider>
      </I18nProvider>
    </QueryClientProvider>,
  );
  showModelLevelSettings();
  return { saved };
}

// The affinity control lives behind the "model-level settings" fold, like every
// other override this dialog exposes, so every case has to open it first.
function showModelLevelSettings() {
  fireEvent.click(
    screen.getByRole("button", { name: "Show model-level settings" }),
  );
}

// Several selects in this dialog offer an "Inherit" option, so the option text
// alone cannot identify the affinity field — walk down from its own label.
function stickySelect(): HTMLSelectElement {
  const label = screen.getByText("Sticky session").closest("label");
  const select = label?.querySelector("select");
  if (!select) throw new Error("sticky session field not rendered");
  return select as HTMLSelectElement;
}

// Read the ladder from the affinity select's own options: several other selects
// in this dialog also offer an "Inherit" entry, so a document-wide query is
// ambiguous.
function inheritLabel(): string {
  return stickySelect().options[0]?.textContent ?? "";
}

function save() {
  fireEvent.click(screen.getByRole("button", { name: "Save" }));
}

describe("route-level session affinity", () => {
  beforeEach(() => {
    localStorage.clear();
    sessionStorage.clear();
    localStorage.setItem("meta-gateway.locale", "en");
    localStorage.setItem("meta-gateway.admin-token", "test-token");
  });
  afterEach(() => { cleanup(); });

  // Affinity is a per-model trade-off, so the dialog has to expose the
  // three-state override rather than only a global switch.
  it("maps on, off and inherit onto the tri-state field", async () => {
    const { saved } = renderDialog({ id: 1, model_pattern: "unified" });
    const select = stickySelect();

    fireEvent.change(select, { target: { value: "on" } });
    save();
    expect(saved[0]?.sticky_session).toBe(true);

    fireEvent.change(select, { target: { value: "off" } });
    save();
    expect(saved[1]?.sticky_session).toBe(false);

    // Back to inherit: null is what the backend reads as "follow the global
    // setting", and omitting the key would be a different statement on a
    // full-replacement PUT.
    fireEvent.change(select, { target: { value: "inherit" } });
    save();
    expect(saved[2]?.sticky_session).toBeNull();
  });

  it("starts on inherit when the route carries no override", async () => {
    renderDialog({ id: 1, model_pattern: "unified" });
    expect(stickySelect().value).toBe("inherit");
  });

  // "Inherit" is only useful if the operator can see what is being inherited;
  // the global switch may well be off, in which case inheriting means off.
  it("names the state the inherited option resolves to", async () => {
    renderDialog({ id: 1, model_pattern: "unified" }, { stickyGlobalDefault: false });
    expect(inheritLabel()).toBe("Inherit (Disabled)");
  });

  it("names the enabled default too", async () => {
    renderDialog({ id: 1, model_pattern: "unified" }, { stickyGlobalDefault: true });
    expect(inheritLabel()).toBe("Inherit (Enabled)");
  });

  it("falls back to a bare inherit while the global setting loads", async () => {
    renderDialog({ id: 1, model_pattern: "unified" }, { stickyGlobalDefault: null });
    expect(inheritLabel()).toBe("Inherit");
  });
});
