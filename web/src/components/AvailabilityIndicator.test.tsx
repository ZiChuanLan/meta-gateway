import { cleanup, render } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, expect, it } from "vitest";
import type { AvailabilityItem, AvailabilitySeries } from "../api/types";
import { I18nProvider } from "../i18n";
import { AvailabilityIndicator } from "./AvailabilityIndicator";

afterEach(cleanup);

const window24 = (overrides: Partial<AvailabilitySeries>): AvailabilitySeries => ({
  source: "traffic",
  scope: "member",
  ratio: 0.969,
  samples: 33,
  ok: 32,
  failed: 1,
  ignored: 0,
  stale: false,
  buckets: Array.from({ length: 24 }, (_, index) => ({
    at: new Date(Date.UTC(2026, 9, 11, index)).toISOString(),
    ok: index === 5 ? 32 : 0,
    failed: index === 5 ? 1 : 0,
    samples: index === 5 ? 33 : 0,
    ignored: 0,
  })),
  paths: [],
  ...overrides,
});

function renderIndicator(item?: AvailabilityItem) {
  return render(
    <I18nProvider>
      <MemoryRouter>
        <AvailabilityIndicator item={item} />
      </MemoryRouter>
    </I18nProvider>,
  );
}

// The readout is one percentage plus a shape. Everything that used to be a
// caption line under it (source, caveats) now lives in the hover title and in
// the dialog, so a list of rows stays a list of numbers.
it("draws the time shape as vertical ticks and never a caption line", () => {
  const { container } = renderIndicator({
    model: "preview-model-01",
    primary: window24({}),
    evidence: [],
  });
  expect(container.querySelectorAll(".availability-bar")).toHaveLength(24);
  expect(container.querySelector(".availability-caption")).toBeNull();
  expect(container.querySelector(".availability-ratio")).toBeNull();
  expect(container.querySelector(".availability-gauge")).toBeNull();
  expect(container.querySelector(".availability-readout > strong")?.textContent).toBe("96.9%");
  expect(container.querySelector(".availability-indicator")?.getAttribute("title")).toContain(
    "32/33",
  );
});

// A number without a timeline must not be drawn as empty time buckets; it gets
// one vertical bar with the same bands the ticks use.
it("uses a single vertical gauge when the source publishes no timeline", () => {
  const { container } = renderIndicator({
    model: "agg-model",
    primary: window24({
      source: "site",
      ratio: 0.4,
      samples: 0,
      ok: 0,
      failed: 0,
      buckets: [],
    }),
    evidence: [],
  });
  expect(container.querySelectorAll(".availability-bar")).toHaveLength(0);
  expect(container.querySelectorAll(".availability-gauge")).toHaveLength(1);
  expect(container.querySelector(".availability-gauge.is-failed")).not.toBeNull();
  expect(container.querySelector(".availability-caption")).toBeNull();
});

it("shows only a dash when there is no evidence", () => {
  const { container } = renderIndicator({ model: "empty", primary: null, evidence: [] });
  expect(container.querySelector(".availability-readout > strong")?.textContent).toBe("—");
  expect(container.querySelectorAll(".availability-bar")).toHaveLength(0);
  expect(container.querySelectorAll(".availability-gauge")).toHaveLength(0);
  expect(container.querySelector(".availability-caption")).toBeNull();
});
