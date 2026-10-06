import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { RouteOrderDialog } from "./RouteOrderDialog";
import type { RouteOrder } from "../team/types";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
beforeEach(() => {
  localStorage.clear();
  localStorage.setItem("meta-gateway.locale", "en");
});

const order: RouteOrder = {
  model: "gpt-4o-mini",
  plan_id: 0,
  customized: false,
  upstreams: [
    {
      id: 11,
      channel_id: 1,
      channel: "Site A",
      origin: "",
      group: "default",
      site_priority: 20,
      site_weight: 100,
      site_enabled: true,
      weight: 100,
      disabled: false,
    },
    {
      id: 12,
      channel_id: 2,
      channel: "Site B",
      origin: "gpt-4o-2024-08-06",
      group: "default",
      site_priority: 10,
      site_weight: 100,
      site_enabled: true,
      weight: 100,
      disabled: false,
    },
  ],
};

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}

function mount() {
  const calls: Array<[string, RequestInit | undefined]> = [];
  vi.stubGlobal(
    "fetch",
    vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
      calls.push([String(input), init]);
      if (String(input).startsWith("/me/routes/gpt-4o-mini")) return json(order);
      return json({ ok: true });
    }),
  );
  render(
    <QueryClientProvider
      client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
    >
      <I18nProvider>
        <RouteOrderDialog
          model="gpt-4o-mini"
          planId={0}
          locale="en"
          onClose={() => undefined}
          onSaved={() => undefined}
        />
      </I18nProvider>
    </QueryClientProvider>,
  );
  return calls;
}

const rowNames = () =>
  [...document.querySelectorAll(".route-order-name strong")].map((node) =>
    node.textContent?.trim(),
  );

describe("upstream arrangement", () => {
  it("shows the site's order and drags a row to the front", async () => {
    mount();
    await waitFor(() => expect(rowNames()).toEqual(["Site A", "Site B"]));

    const rows = screen.getAllByRole("listitem");
    fireEvent.dragStart(rows[1]!);
    fireEvent.dragOver(rows[0]!);
    fireEvent.drop(rows[0]!);

    // The array IS the failover order: moving the row moves the first choice.
    expect(rowNames()).toEqual(["Site B", "Site A"]);
  });

  it("saves the visible order and the switched-off rows as one list", async () => {
    const calls = mount();
    await waitFor(() => expect(rowNames()).toEqual(["Site A", "Site B"]));

    const rows = screen.getAllByRole("listitem");
    fireEvent.dragStart(rows[1]!);
    fireEvent.dragOver(rows[0]!);
    fireEvent.drop(rows[0]!);
    // Site B now leads the list, so its switch is the first one.
    fireEvent.click(screen.getAllByRole("checkbox")[0]!);
    fireEvent.click(screen.getByRole("button", { name: "Save" }));

    await waitFor(() => {
      const put = calls.find(([, init]) => init?.method === "PUT");
      expect(put).toBeTruthy();
      const body = JSON.parse(String(put![1]!.body)) as {
        plan_id: number;
        entries: Array<{ id: number; weight: number; disabled?: boolean }>;
      };
      expect(body.plan_id).toBe(0);
      expect(body.entries.map((entry) => entry.id)).toEqual([12, 11]);
      // Site B moved to the top and was then switched off: still listed, out
      // of routing.
      expect(body.entries[0]).toEqual({ id: 12, weight: 100, disabled: true });
    });
  });

  it("offers the site order again once the model is arranged", async () => {
    vi.stubGlobal(
      "fetch",
      vi.fn((input: RequestInfo | URL, init?: RequestInit) => {
        if (String(input).startsWith("/me/routes/")) {
          if (init?.method === "DELETE") return json({ ok: true });
          return json({ ...order, customized: true, plan_id: 3 });
        }
        return json({ ok: true });
      }),
    );
    render(
      <QueryClientProvider
        client={new QueryClient({ defaultOptions: { queries: { retry: false } } })}
      >
        <I18nProvider>
          <RouteOrderDialog
            model="gpt-4o-mini"
            planId={3}
            locale="en"
            onClose={() => undefined}
            onSaved={() => undefined}
          />
        </I18nProvider>
      </QueryClientProvider>,
    );
    expect(await screen.findByRole("button", { name: "Restore site order" })).toBeInTheDocument();
  });
});
