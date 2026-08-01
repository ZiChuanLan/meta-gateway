import { useEffect, useMemo, useState } from "react";

export const DEFAULT_PAGE_SIZE = 20;
export const PAGE_SIZE_OPTIONS = [20, 50, 100] as const;

/**
 * Client-side pagination for already-fetched admin lists.
 * Resets to page 1 when the filtered item count or page size changes
 * in a way that makes the current page empty.
 */
export function useClientPagination<T>(
	items: readonly T[],
	initialPageSize: number = DEFAULT_PAGE_SIZE,
) {
	const [page, setPage] = useState(1);
	const [pageSize, setPageSize] = useState(initialPageSize);

	const total = items.length;
	const totalPages = Math.max(1, Math.ceil(total / pageSize) || 1);
	const safePage = Math.min(Math.max(1, page), totalPages);

	useEffect(() => {
		if (page !== safePage) setPage(safePage);
	}, [page, safePage]);

	// When filters shrink the list, jump back to a valid page.
	useEffect(() => {
		setPage(1);
	}, [total, pageSize]);

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
