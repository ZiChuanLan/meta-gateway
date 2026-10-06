import { expect, it } from "vitest";
import { compareVersions, updateLanded, updateState, updateWentBackwards } from "./updateState";
const base = {
  enabled: true,
  current: "v4.0.0-beta.10",
  latest: "v4.0.0-beta.2",
  has_update: false,
  release_url: "",
};
it("distinguishes unavailable comparison from genuinely current or ahead builds", () => {
  expect(updateState(base)).toBe("ahead");
  expect(updateState({ ...base, current: "dev" })).toBe("uncomparable");
  expect(updateState({ ...base, enabled: false })).toBe("disabled");
  expect(updateState({ ...base, latest: "" })).toBe("unchecked");
  expect(updateState({ ...base, error: "network" })).toBe("checkFailed");
  expect(updateState({ ...base, latest: "v4.0.0" })).toBe("available");
  expect(updateState({ ...base, latest: base.current })).toBe("current");
});

it("orders prereleases below the release of the same core and above earlier betas", () => {
  expect(compareVersions("v4.0.0-beta.10", "v4.0.0-beta.2")).toBe(1);
  expect(compareVersions("v4.0.0", "v4.0.0-beta.10")).toBe(1);
  expect(compareVersions("v3.8.6", "v4.0.0-beta.1")).toBe(-1);
  expect(compareVersions("v4.0.0-beta.2", "v4.0.0-beta.2")).toBe(0);
  // Unparseable is not "equal": a dev build must never look like a match.
  expect(compareVersions("dev", "v4.0.0")).toBeNull();
});

// v4.0.0-beta.6 is the shape this exists for: the image and the floating `beta`
// tag were pushed, no GitHub release was created for the tag, so the beta
// channel offered beta.5 while the executor would install beta.6. Waiting for
// `/healthz` to equal the offered version never succeeded, and the dialog
// reported a timeout for an update that had actually worked.
it("accepts any newer build on a tracked tag, but only the exact build otherwise", () => {
  const tracked = { target: "v4.0.0-beta.5", from: "v4.0.0-beta.4", tracked: "beta" };
  expect(updateLanded(tracked, "v4.0.0-beta.5")).toBe(true);
  expect(updateLanded(tracked, "v4.0.0-beta.6")).toBe(true);
  expect(updateLanded(tracked, "v4.0.0")).toBe(true);
  // Nothing has happened yet, the build is older, or it cannot be compared.
  expect(updateLanded(tracked, "v4.0.0-beta.4")).toBe(false);
  expect(updateLanded(tracked, "v4.0.0-beta.3")).toBe(false);
  expect(updateLanded(tracked, "dev")).toBe(false);
  expect(updateLanded(tracked, undefined)).toBe(false);
  // Without a starting version there is nothing to compare against, so the
  // tracked path falls back to requiring the named release.
  expect(updateLanded({ ...tracked, from: undefined }, "v4.0.0-beta.6")).toBe(false);
  expect(updateLanded({ ...tracked, from: undefined }, "v4.0.0-beta.5")).toBe(true);

  // The socket path pulls the release it was asked for, so anything else is not
  // confirmation — including a newer build.
  const exact = { target: "v4.0.0-beta.5", from: "v4.0.0-beta.4" };
  expect(updateLanded(exact, "v4.0.0-beta.5")).toBe(true);
  expect(updateLanded(exact, "v4.0.0-beta.6")).toBe(false);
});

it("reports a tracked tag that came back older instead of waiting out the budget", () => {
  const watch = { from: "v4.0.0-beta.6", tracked: "beta" };
  expect(updateWentBackwards(watch, "v4.0.0-beta.5")).toBe(true);
  expect(updateWentBackwards(watch, "v3.8.6")).toBe(true);
  expect(updateWentBackwards(watch, "v4.0.0-beta.6")).toBe(false);
  expect(updateWentBackwards(watch, "v4.0.0-beta.7")).toBe(false);
  // Only the tracked path has this failure shape; the socket path installs what
  // it was asked to and reports the mismatch instead.
  expect(updateWentBackwards({ from: "v4.0.0-beta.6" }, "v3.8.6")).toBe(false);
});
