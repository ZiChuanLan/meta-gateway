import { useEffect, useMemo, useState } from "react";

const DEFAULT_PAGE_SIZE = 20;
export const PAGE_SIZE_OPTIONS = [20, 50, 100] as const;

/**
 * Client-side pagination for already-fetched admin lists.
 * Restores page/page-size within the current browser tab when storageKey is
 * supplied. The current page is clamped when the filtered list becomes shorter.
 */
export function useClientPagination<T>(
  items: readonly T[],
  initialPageSize: number = DEFAULT_PAGE_SIZE,
  storageKey?: string,
) {
  const saved = useMemo(() => {
    if (!storageKey || typeof sessionStorage === "undefined") return null;
    try {
      const raw = sessionStorage.getItem(`pagination:${storageKey}`);
      return raw
        ? (JSON.parse(raw) as { page?: number; pageSize?: number })
        : null;
    } catch {
      return null;
    }
  }, [storageKey]);
  const [page, setPage] = useState(
    saved?.page && saved.page > 0 ? saved.page : 1,
  );
  const [pageSize, setPageSize] = useState(
    saved?.pageSize &&
      PAGE_SIZE_OPTIONS.includes(
        saved.pageSize as (typeof PAGE_SIZE_OPTIONS)[number],
      )
      ? saved.pageSize
      : initialPageSize,
  );

  const total = items.length;
  const totalPages = Math.max(1, Math.ceil(total / pageSize) || 1);
  // Keep a restored page while the query is still loading (total=0); once
  // data arrives, clamp it to the real page count instead of losing it.
  const safePage =
    total === 0 ? Math.max(1, page) : Math.min(Math.max(1, page), totalPages);

  useEffect(() => {
    if (page !== safePage) setPage(safePage);
  }, [page, safePage]);

  useEffect(() => {
    if (!storageKey || typeof sessionStorage === "undefined") return;
    try {
      sessionStorage.setItem(
        `pagination:${storageKey}`,
        JSON.stringify({ page: safePage, pageSize }),
      );
    } catch {
      // Storage is optional; pagination remains functional in memory.
    }
  }, [pageSize, safePage, storageKey]);

  const pageItems = useMemo(() => {
    const start = (safePage - 1) * pageSize;
    return items.slice(start, start + pageSize) as T[];
  }, [items, safePage, pageSize]);

  const rangeStart = total === 0 ? 0 : (safePage - 1) * pageSize + 1;
  const rangeEnd = Math.min(safePage * pageSize, total);

  return {
    page: safePage,
    setPage,
    pageSize,
    setPageSize: (size: number) => {
      setPageSize(size);
    },
    total,
    totalPages,
    pageItems,
    rangeStart,
    rangeEnd,
    hasPrev: safePage > 1,
    hasNext: safePage < totalPages,
  };
}
