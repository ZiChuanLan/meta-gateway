import type { ReactNode } from "react";

/**
 * Ops list chrome: scrollable body + pinned footer (pagination).
 * Keeps PaginationBar outside the overflow region.
 */
export function ListShell({
	children,
	footer,
	className = "",
}: {
	children: ReactNode;
	footer?: ReactNode;
	className?: string;
}) {
	return (
		<div className={["list-shell", className].filter(Boolean).join(" ")}>
			<div className="list-shell-body">{children}</div>
			{footer ? <div className="list-shell-footer">{footer}</div> : null}
		</div>
	);
}
