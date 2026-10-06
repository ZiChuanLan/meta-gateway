import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { SessionProvider } from "../session";
import { UpdateDialog } from "./UpdateDialog";
import type { UpdateCheckStatus } from "../api/types";
import type { ReactNode } from "react";

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
	localStorage.clear();
	sessionStorage.clear();
});

it("resumes observing an update after remount without submitting it twice", async () => {
  const fetcher = stubFetch({ latest: "v4.0.0-beta.2", has_update: true });
  vi.stubGlobal("fetch", fetcher);
  const props = { ...noUpdate, latest: "v4.0.0-beta.2", has_update: true };
  const first = mount(<UpdateDialog update={props} onClose={() => {}} />);
  const apply = await screen.findByRole("button", { name: /Update to v4\.0\.0-beta\.2/ });
  await waitFor(() => expect(apply).toBeEnabled());
  fireEvent.click(apply);
  await waitFor(() => expect(sessionStorage.getItem("meta-gateway.update-watch")).not.toBeNull());
  const before = sessionStorage.getItem("meta-gateway.update-watch");
  first.unmount();
  mount(<UpdateDialog update={props} onClose={() => {}} />);
  expect(sessionStorage.getItem("meta-gateway.update-watch")).toBe(before);
  expect(fetcher.mock.calls.filter(([path]) => String(path).includes("/self-update/apply"))).toHaveLength(1);
});

it("shows the persisted failure when reopening without starting another update", async () => {
  const fetcher = stubFetch({}, { phase: "failed", error: "previous update failed; inspect the deployment logs" });
  vi.stubGlobal("fetch", fetcher);
  mount(<UpdateDialog update={noUpdate} onClose={() => {}} />);
  expect(await screen.findByRole("alert")).toHaveTextContent("previous update failed");
  expect(fetcher.mock.calls.some(([path]) => String(path).includes("/self-update/apply"))).toBe(false);
});

function mount(children: ReactNode) {
	localStorage.setItem("meta-gateway.locale", "en");
	localStorage.setItem("meta-gateway.admin-token", "session-token");
	return render(
		<QueryClientProvider
			client={new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } })}
		>
			<I18nProvider>
				<SessionProvider>{children}</SessionProvider>
			</I18nProvider>
		</QueryClientProvider>,
	);
}

/** Answer the three reads the dialog makes; the check result is the interesting one. */
function stubFetch(
  check: Partial<UpdateCheckStatus>,
  selfUpdate: Record<string, unknown> = {},
  channelDoc: Record<string, unknown> = {},
) {
  return vi.fn(async (input: RequestInfo | URL) => {
    const path = String(input).split("?")[0];
    if (path === "/admin/update-check")
      return new Response(
        JSON.stringify({
          channel: "stable",
          enabled: true,
          current: "v4.0.0-beta.1",
          latest: "v3.8.6",
          has_update: false,
          release_url: "",
          ...check,
        }),
      );
    if (path === "/admin/update-channel")
      return new Response(
        JSON.stringify({
          channel: "stable",
          mode: "socket",
          tracking_tag: "beta",
          tracking_channel: "",
          ...channelDoc,
        }),
      );
    if (path === "/admin/self-update")
      return new Response(JSON.stringify({ available: true, running: false, phase: "idle", ...selfUpdate }));
    return new Response(JSON.stringify({}), { status: 200 });
  });
}

const noUpdate: UpdateCheckStatus = {
	channel: "stable",
	enabled: true,
	current: "v4.0.0-beta.1",
	latest: "v3.8.6",
	has_update: false,
	release_url: "",
};

// Switching to the stable channel while running a beta does not downgrade, so
// there is genuinely nothing to install. The dialog used to answer that with a
// bare "no upgrade is available", which an operator reads as "I am on stable" —
// the running version was not shown anywhere at all.
it("says the running build is ahead of the selected channel instead of only 'no upgrade'", async () => {
	vi.stubGlobal("fetch", stubFetch({}));
	mount(<UpdateDialog update={noUpdate} onClose={() => {}} />);

	expect(await screen.findByText(/Running v4\.0\.0-beta\.1/)).toBeInTheDocument();
	expect(
		await screen.findByText(/newer than the latest release on the Stable channel \(v3\.8\.6\)/),
	).toBeInTheDocument();
	// And the title must not promise the older version.
	expect(screen.queryByText(/Update to v3\.8\.6/)).toBeNull();
});

it("shows a plain 'no upgrade' when the running build IS the channel's latest", async () => {	vi.stubGlobal(
		"fetch",
		stubFetch({ current: "v3.8.6", latest: "v3.8.6" }),
	);
	mount(
		<UpdateDialog
			update={{ ...noUpdate, current: "v3.8.6", latest: "v3.8.6" }}
			onClose={() => {}}
		/>,
	);

	expect(await screen.findByText(/No upgrade is available/)).toBeInTheDocument();
	expect(screen.queryByText(/newer than the latest release/)).toBeNull();
});

// The handoff can fail fast — a socket it cannot open, an image it cannot pull —
// and /admin/self-update is the only place that says why. Reporting "not
// confirmed in time" three minutes later hid a permission error behind a
// timeout.
it("reports the updater's own failure reason instead of a timeout", { timeout: 20000 }, async () => {
	const fetcher = stubFetch(
		{ latest: "v4.0.0-beta.2", has_update: true },
		{
			phase: "failed",
			error:
				'inspect self: Get "http://dockerv1.41/containers/abc/json": dial unix /var/run/docker.sock: connect: permission denied',
		},
	);
	vi.stubGlobal("fetch", fetcher);
	mount(
		<UpdateDialog
			update={{ ...noUpdate, latest: "v4.0.0-beta.2", has_update: true }}
			onClose={() => {}}
		/>,
	);

	// The button renders immediately but stays disabled until the check settles, so
	// the click has to wait for it to become usable.
	const apply = await screen.findByRole("button", { name: /Update to v4\.0\.0-beta\.2/ });
	await waitFor(() => expect(apply).toBeEnabled());
	fireEvent.click(apply);

	// One poll tick is enough: the failure is reported as soon as it is seen,
	// rather than after the three-minute timeout. (The hook's tick is 3s.)
	const alert = await screen.findByRole("alert", undefined, { timeout: 8000 });
	expect(alert).toHaveTextContent(/permission denied/);
	// The raw message names neither the cause nor the fix, so the dialog adds both.
	expect(alert).toHaveTextContent(/group_add/);
	expect(alert).not.toHaveTextContent(/not confirmed in time/);
	expect(fetcher.mock.calls.some(([p]) => String(p).includes("/admin/self-update"))).toBe(true);
});

// The watchtower executor updates one floating tag, and that tag is declared in
// the deployment file — not in the console. Offering a cross-track install can
// only end in the server's watchtower_channel_mismatch, so the dialog prepares
// the operator with the value to change instead.
it("refuses a cross-track install and names the deployment value to change", async () => {
  vi.stubGlobal(
    "fetch",
    stubFetch(
      { channel: "stable", current: "v3.8.6", latest: "v4.0.0-beta.7", has_update: true },
      {
        available: true,
        running: false,
        phase: "idle",
        mode: "watchtower",
        tracking_tag: "beta",
        tracking_channel: "beta",
      },
      { channel: "stable", mode: "watchtower", tracking_tag: "beta", tracking_channel: "beta" },
    ),
  );
  mount(
    <UpdateDialog
      update={{
        ...noUpdate,
        channel: "stable",
        current: "v3.8.6",
        latest: "v4.0.0-beta.7",
        has_update: true,
      }}
      onClose={() => {}}
    />,
  );

  // The button names what the executor will actually do — install the newest
  // build on the tracked tag — instead of promising a release it may not install.
  const apply = await screen.findByRole("button", { name: /Install the newest build on beta/ });
  await waitFor(() => expect(apply).toBeDisabled());
  expect(await screen.findByText(/set IMAGE_TAG to the other tag/)).toBeInTheDocument();
});
