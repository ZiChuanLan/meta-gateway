import { useState } from "react";
import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { I18nProvider } from "../i18n";
import { ENTRANCE_CHARGE_MS, ENTRANCE_REVEAL_MS } from "../lib/entranceMotion";
import { GatewayPreview } from "./GatewayTransition";

beforeEach(() => {
  localStorage.setItem("meta-gateway.locale", "en");
  vi.useFakeTimers();
  vi.stubGlobal("matchMedia", vi.fn(() => ({ matches: false, addEventListener: vi.fn(), removeEventListener: vi.fn() })));
});
afterEach(() => { cleanup(); localStorage.clear(); vi.useRealTimers(); vi.unstubAllGlobals(); });

function Preview() {
  const [open, setOpen] = useState(false);
  return <><button onClick={() => setOpen(true)}>Replay entrance</button>{open ? <GatewayPreview onClose={() => setOpen(false)} /> : null}</>;
}

it("replays and restores focus without touching authentication or making requests", () => {
  const fetch = vi.fn();
  vi.stubGlobal("fetch", fetch);
  localStorage.setItem("meta-gateway.admin-token", "existing-session");
  render(<I18nProvider><Preview /></I18nProvider>);
  const trigger = screen.getByRole("button", { name: "Replay entrance" });
  trigger.focus(); fireEvent.click(trigger);
  expect(screen.getByRole("dialog", { name: "Workspace entrance" })).toBeInTheDocument();
  act(() => vi.advanceTimersByTime(ENTRANCE_CHARGE_MS));
  expect(document.querySelector(".gateway-cinematic.is-revealing")).toBeInTheDocument();
  act(() => vi.advanceTimersByTime(ENTRANCE_REVEAL_MS));
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  expect(trigger).toHaveFocus();
  expect(localStorage.getItem("meta-gateway.admin-token")).toBe("existing-session");
  expect(fetch).not.toHaveBeenCalled();
  expect(document.body.style.overflow).not.toBe("hidden");
});

it("skips immediately with Escape and cleans up pending animation timers", () => {
  render(<I18nProvider><Preview /></I18nProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Replay entrance" }));
  fireEvent.keyDown(window, { key: "Escape" });
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  act(() => vi.advanceTimersByTime(4000));
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});

it("uses the short path for reduced motion", () => {
  vi.stubGlobal("matchMedia", vi.fn(() => ({ matches: true, addEventListener: vi.fn(), removeEventListener: vi.fn() })));
  render(<I18nProvider><Preview /></I18nProvider>);
  fireEvent.click(screen.getByRole("button", { name: "Replay entrance" }));
  act(() => vi.advanceTimersByTime(160));
  expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
});
