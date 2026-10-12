import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { useState, type ReactNode } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../../i18n";
import { ModelDirectoryTable } from "./ModelDirectoryTable";
import { ModelWorkspaceLayout } from "./ModelWorkspaceLayout";

beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("meta-gateway.locale", "en");
});
afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

/**
 * A controllable `matchMedia`. The phone tests need to flip the breakpoint
 * mid-test and to see the listener torn down, so this one records its listeners
 * and can replay a `change` — `matches` starts at the requested width.
 */
function stubMobile(matches: boolean) {
  const listeners = new Set<(event: MediaQueryListEvent) => void>();
  const media = {
    matches,
    media: "(max-width: 760px)",
    addEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) => {
      listeners.add(listener);
    },
    removeEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) => {
      listeners.delete(listener);
    },
    addListener: (listener: (event: MediaQueryListEvent) => void) => listeners.add(listener),
    removeListener: (listener: (event: MediaQueryListEvent) => void) => listeners.delete(listener),
  };
  vi.stubGlobal(
    "matchMedia",
    vi.fn(() => media),
  );
  return {
    resize(next: boolean) {
      media.matches = next;
      act(() => {
        for (const listener of [...listeners]) listener({ matches: next } as MediaQueryListEvent);
      });
    },
    listenerCount: () => listeners.size,
  };
}

/** A parent that owns the selection, the way the Models page does: the row's
 *  own click updates it, and the detail shows what was just picked. */
function SelectingHarness() {
  const [name, setName] = useState<string | null>(null);
  return (
    <I18nProvider>
      <ModelWorkspaceLayout
        directory={
          <ModelDirectoryTable
            showUpstream={false}
            rows={["alpha", "beta"].map((model) => ({
              name: model,
              status: "enabled" as const,
              onClick: () => setName(model),
              tabIndex: 0,
            }))}
          />
        }
        detail={<p>Detail for {name ?? "none"}</p>}
      />
    </I18nProvider>
  );
}

function PhoneManagement({ directory }: { directory?: ReactNode }) {
  return (
    <I18nProvider>
      <ModelWorkspaceLayout
        directory={
          directory ?? (
            <ModelDirectoryTable
              showUpstream={false}
              rows={[
                { name: "plugin-model", status: "enabled" },
                { name: "route-model", status: "enabled", onClick: vi.fn(), tabIndex: 0 },
              ]}
            />
          )
        }
        detail={<p>Routing editor</p>}
      />
    </I18nProvider>
  );
}

it("opens route details on a card without opening them for plugin-only cards", () => {
  render(
    <I18nProvider>
      <ModelWorkspaceLayout
        variant="cards"
        directory={
          <ModelDirectoryTable
            showUpstream={false}
            rows={[
              { name: "plugin-model", status: "enabled" },
              { name: "route-model", status: "enabled", onClick: vi.fn(), tabIndex: 0 },
            ]}
          />
        }
        detail={<p>Route actions</p>}
      />
    </I18nProvider>,
  );
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.click(screen.getByText("plugin-model"));
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.click(screen.getByText("route-model"));
  expect(screen.getByRole("dialog")).toHaveTextContent("Route actions");
});

it("keeps management in the original persistent list/detail layout by default", () => {
  render(
    <I18nProvider>
      <ModelWorkspaceLayout directory={<p>Management list</p>} detail={<p>Routing editor</p>} />
    </I18nProvider>,
  );
  expect(screen.getByText("Routing editor")).toBeInTheDocument();
  expect(document.querySelector(".models-split .ops-detail-card")).toBeInTheDocument();
  expect(document.querySelector(".model-card-workspace")).toBeNull();
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("turns the management page into a directory plus a detail dialog on a phone", () => {
  stubMobile(true);
  render(<PhoneManagement />);
  // The list is the page; the split's detail pane is not rendered at all, and
  // the phone list starts closed even though nothing is selected yet.
  expect(screen.getByText("route-model")).toBeInTheDocument();
  expect(document.querySelector(".models-split .ops-detail-card")).toBeNull();
  expect(screen.queryByRole("dialog")).toBeNull();
  // A plugin-only row owns no route to open.
  fireEvent.click(screen.getByText("plugin-model"));
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.click(screen.getByText("route-model"));
  const dialog = screen.getByRole("dialog");
  expect(dialog).toHaveTextContent("Routing editor");
  expect(dialog).toHaveTextContent("Back to model directory");
  // Escape is the dialog's own convention, kept by the shared component.
  fireEvent.keyDown(window, { key: "Escape" });
  expect(screen.queryByRole("dialog")).toBeNull();
});

it("runs the selected row's own click and shows the detail it just produced", () => {
  stubMobile(true);
  render(<SelectingHarness />);
  fireEvent.click(screen.getByText("alpha"));
  expect(screen.getByRole("dialog")).toHaveTextContent("Detail for alpha");
  fireEvent.keyDown(window, { key: "Escape" });
  fireEvent.click(screen.getByText("beta"));
  expect(screen.getByRole("dialog")).toHaveTextContent("Detail for beta");
});

it("never opens the phone detail from a price, bulk, link or plain-button cell", () => {
  stubMobile(true);
  render(
    <PhoneManagement
      directory={
        <ModelDirectoryTable
          showUpstream
          bulkMode
          bulkHeader={<input type="checkbox" aria-label="select page" />}
          onBulkCellClick={(event) => event.stopPropagation()}
          rows={[
            {
              name: "guarded-model",
              status: "enabled",
              onClick: vi.fn(),
              tabIndex: 0,
              bulkCell: <input type="checkbox" aria-label="select guarded-model" />,
              prices: <input aria-label="price" />,
              upstream: <a href="#channel">Channel</a>,
              actions: <button type="button">More</button>,
            },
          ]}
        />
      }
    />,
  );
  fireEvent.click(screen.getByLabelText("select guarded-model"));
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.click(screen.getByLabelText("price"));
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.click(screen.getByText("Channel"));
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "More" }));
  expect(screen.queryByRole("dialog")).toBeNull();
  // The row itself is still the way in.
  fireEvent.click(screen.getByText("guarded-model"));
  expect(screen.getByRole("dialog")).toBeInTheDocument();
});

it("opens the phone detail with Enter or Space on a selectable row", () => {
  stubMobile(true);
  render(<PhoneManagement />);
  const row = screen.getByText("route-model").closest("tr")!;
  fireEvent.keyUp(row, { key: "Enter" });
  expect(screen.getByRole("dialog")).toBeInTheDocument();
  fireEvent.keyDown(window, { key: "Escape" });
  expect(screen.queryByRole("dialog")).toBeNull();
  fireEvent.keyUp(row, { key: " " });
  expect(screen.getByRole("dialog")).toBeInTheDocument();
});

it("closes the phone dialog on a resize and never remounts the directory", () => {
  const media = stubMobile(true);
  const directory = (
    <>
      <input aria-label="filter" defaultValue="" />
      <ModelDirectoryTable
        showUpstream={false}
        rows={[{ name: "route-model", status: "enabled", onClick: vi.fn(), tabIndex: 0 }]}
      />
    </>
  );
  const { unmount } = render(<PhoneManagement directory={directory} />);
  const filter = screen.getByLabelText("filter") as HTMLInputElement;
  const table = document.querySelector(".model-directory-table");
  fireEvent.change(filter, { target: { value: "gpt" } });

  fireEvent.click(screen.getByText("route-model"));
  expect(screen.getByRole("dialog")).toBeInTheDocument();
  media.resize(false);
  expect(screen.queryByRole("dialog")).toBeNull();
  // Desktop again: the detail is the persistent pane, and the directory the
  // operator typed into is the same node with the same filter value.
  expect(document.querySelector(".models-split .ops-detail-card")).toHaveTextContent(
    "Routing editor",
  );
  expect(document.querySelector(".model-directory-table")).toBe(table);
  expect(filter.value).toBe("gpt");
  // Back to a phone: the selection survives as state, the dialog does not.
  media.resize(true);
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(document.querySelector(".models-split .ops-detail-card")).toBeNull();

  expect(media.listenerCount()).toBe(1);
  unmount();
  expect(media.listenerCount()).toBe(0);
});
