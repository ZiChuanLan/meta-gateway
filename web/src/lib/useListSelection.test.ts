import { act, renderHook } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { useListSelection } from "./useListSelection";

describe("useListSelection", () => {
  it("ticks and unticks one row at a time", () => {
    const { result } = renderHook(() => useListSelection());
    act(() => result.current.toggle(3));
    act(() => result.current.toggle(7));
    expect([...result.current.selected].sort()).toEqual([3, 7]);
    act(() => result.current.toggle(3));
    expect([...result.current.selected]).toEqual([7]);
  });

  it("leaves no selection behind when bulk mode is left", () => {
    const { result } = renderHook(() => useListSelection());
    act(() => result.current.setMode(true));
    act(() => result.current.toggle(1));
    act(() => result.current.toggle(2));
    expect(result.current.selected.size).toBe(2);
    act(() => result.current.exit());
    expect(result.current.mode).toBe(false);
    expect(result.current.selected.size).toBe(0);
  });

  it("replaces the selection wholesale for select-all and keep-only-failures", () => {
    const { result } = renderHook(() => useListSelection());
    act(() => result.current.toggle(9));
    act(() => result.current.replace([4, 5]));
    expect([...result.current.selected].sort()).toEqual([4, 5]);
    act(() => result.current.clear());
    expect(result.current.selected.size).toBe(0);
  });

  it("supports a functional update from a menu handler", () => {
    const { result } = renderHook(() => useListSelection());
    act(() => result.current.setSelected((current) => new Set(current).add(11)));
    expect([...result.current.selected]).toEqual([11]);
  });
});
