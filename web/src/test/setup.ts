import "@testing-library/jest-dom/vitest";
import { configure } from "@testing-library/react";

// jsdom implements neither `showModal` nor `close` on <dialog>, so any board
// whose editor is a TeamModal (tenant groups, access policies) would throw as
// soon as it opened. The polyfill only records the open state — the parts a
// test can assert on are the fields and the request the form sends.
if (typeof HTMLDialogElement !== "undefined" && !HTMLDialogElement.prototype.showModal) {
  HTMLDialogElement.prototype.showModal = function showModal(this: HTMLDialogElement) {
    this.open = true;
  };
  HTMLDialogElement.prototype.close = function close(this: HTMLDialogElement) {
    this.open = false;
  };
}

// `findBy*` and `waitFor` default to a 1000ms wait, which is a figure measured
// against a test file running on its own. This suite runs 52 files in
// parallel, and `App.test.tsx`'s "hides a navigation entry on request" mounts
// the console shell at /models, whose heading takes ~1.2s to paint even when
// nothing else is running. Under full-suite load that wait expired and the file
// failed with `Unable to find role="heading" and name "Models"` — while passing
// on its own and passing when run beside the one other file it was checked
// against.
//
// 3000ms keeps the assertion a real one; it just stops it measuring the
// scheduler's mood instead of the app's behaviour.
configure({ asyncUtilTimeout: 3000 });

// jsdom's matchMedia hands back a MediaQueryList without the listener methods,
// and the appearance layer follows the desktop's colour scheme through exactly
// those. Patching the result once is enough for every component that mounts
// AppearanceProvider in a test.
if (typeof window !== "undefined" && typeof window.matchMedia === "function") {
  const original = window.matchMedia.bind(window);
  window.matchMedia = ((query: string) => {
    const list = original(query);
    if (typeof (list as MediaQueryList).addEventListener !== "function") {
      const listeners = new Set<(event: MediaQueryListEvent) => void>();
      Object.assign(list, {
        addEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) => listeners.add(listener),
        removeEventListener: (_type: string, listener: (event: MediaQueryListEvent) => void) => listeners.delete(listener),
        addListener: (listener: (event: MediaQueryListEvent) => void) => listeners.add(listener),
        removeListener: (listener: (event: MediaQueryListEvent) => void) => listeners.delete(listener),
      });
    }
    return list;
  }) as typeof window.matchMedia;
}
