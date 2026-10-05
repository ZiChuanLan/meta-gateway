import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import {
  PRELOAD_EXHAUSTED_EVENT,
  installPreloadRecovery,
} from "./preloadRecovery";

/**
 * The handler is installed on a stand-in window: jsdom's own `location` is not
 * configurable, and the point of the test is which reload attempts happen.
 */
function fakeWindow() {
  const listeners = new Map<string, (event: unknown) => void>();
  const reload = vi.fn();
  const window = {
    addEventListener: (type: string, handler: (event: unknown) => void) => {
      listeners.set(type, handler);
    },
    dispatchEvent: vi.fn(),
    location: { reload },
    fire: (type: string) => {
      let defaultPrevented = false;
      listeners.get(type)?.({
        preventDefault: () => {
          defaultPrevented = true;
        },
      });
      return defaultPrevented;
    },
  };
  return { window, reload };
}

beforeEach(() => {
  sessionStorage.clear();
});
afterEach(() => {
  vi.restoreAllMocks();
});

describe("stale bundle recovery", () => {
  it("reloads once when a lazily imported chunk is gone", () => {
    const { window, reload } = fakeWindow();
    installPreloadRecovery(window as unknown as Window);

    const prevented = window.fire("vite:preloadError");

    expect(reload).toHaveBeenCalledTimes(1);
    // The browser console stays clean: this is a recovered condition.
    expect(prevented).toBe(true);
  });

  it("stops instead of looping when the reload did not help", () => {
    const { window, reload } = fakeWindow();
    installPreloadRecovery(window as unknown as Window);

    window.fire("vite:preloadError");
    reload.mockClear();
    window.fire("vite:preloadError");

    // A second failure inside the cooldown means reloading again would only
    // make the tab flicker, so the app is told to say something instead.
    expect(reload).not.toHaveBeenCalled();
    expect(window.dispatchEvent).toHaveBeenCalledWith(
      expect.objectContaining({ type: PRELOAD_EXHAUSTED_EVENT }),
    );
  });

  it("recovers again after the cooldown", () => {
    const { window, reload } = fakeWindow();
    installPreloadRecovery(window as unknown as Window);

    window.fire("vite:preloadError");
    reload.mockClear();
    // Pretend the previous recovery was long ago (a second deploy).
    sessionStorage.setItem("meta-gateway.preload-reload", String(Date.now() - 60_000));
    window.fire("vite:preloadError");

    expect(reload).toHaveBeenCalledTimes(1);
  });
});

it("does not enter an automatic reload loop when storage is unavailable",()=>{
 vi.spyOn(Storage.prototype,"setItem").mockImplementation(()=>{throw new Error("storage denied");});
 const {window,reload}=fakeWindow();installPreloadRecovery(window as unknown as Window);
 window.fire("vite:preloadError");window.fire("vite:preloadError");
 expect(reload).not.toHaveBeenCalled();
 expect(window.dispatchEvent).toHaveBeenCalledWith(expect.objectContaining({type:PRELOAD_EXHAUSTED_EVENT}));
});
