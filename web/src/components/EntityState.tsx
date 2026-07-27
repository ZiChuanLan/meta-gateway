import type { ReactNode } from "react";
import { Empty, ErrorState, Loading } from "./ui";

/**
 * First-class loading / empty (with next step) / error for tables and sections.
 */
export function EntityState({
	isLoading,
	isError,
	error,
	isEmpty,
	empty,
	retry,
	children,
}: {
	isLoading?: boolean;
	isError?: boolean;
	error?: unknown;
	isEmpty?: boolean;
	/** Empty copy should name the next operator step when possible. */
	empty?: ReactNode;
	retry?: () => void;
	/** Optional when a terminal loading/error/empty state is shown. */
	children?: ReactNode;
}) {
	if (isLoading) return <Loading />;
	if (isError) return <ErrorState error={error} retry={retry} />;
	if (isEmpty) return <Empty>{empty}</Empty>;
	return <div className="entity-state-fill">{children}</div>;
}
