import { useCallback, useState } from "react";

/**
 * Bulk selection over a list of rows: which ids are ticked, and whether the bulk
 * bar is open.
 *
 * The two belong together, and the invariant is the point: leaving bulk mode
 * clears the ticks as well. Both boards that select rows (connections, models)
 * had grown their own copy of that pair, and the copies disagreed about whether
 * closing the bar kept the selection — so the next "refresh" could act on rows
 * nobody could see were selected any more.
 *
 * `setSelected` accepts a functional update on purpose: the action menus add a row
 * to the current selection from a menu handler, and rebuilding that as a value
 * would either read stale state or force every caller to know the current set.
 */
export type ListSelection = {
  selected: Set<number>;
  mode: boolean;
  setMode: (on: boolean) => void;
  setSelected: (next: Set<number> | ((prev: Set<number>) => Set<number>)) => void;
  toggle: (id: number) => void;
  /** Tick exactly these ids (a page's "select all", or "keep only the failures"). */
  replace: (ids: Iterable<number>) => void;
  clear: () => void;
  /** Leave bulk mode: no mode and no selection left behind. */
  exit: () => void;
};

export function useListSelection(): ListSelection {
  const [selected, setSelected] = useState<Set<number>>(() => new Set());
  const [mode, setMode] = useState(false);

  const toggle = useCallback((id: number) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  }, []);

  return {
    selected,
    mode,
    setMode,
    setSelected,
    toggle,
    replace: (ids) => setSelected(new Set(ids)),
    clear: () => setSelected(new Set()),
    exit: () => {
      setMode(false);
      setSelected(new Set());
    },
  };
}
