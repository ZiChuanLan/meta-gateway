import { ChevronLeft, ChevronRight } from "lucide-react";
import { useI18n } from "../i18n";
import { PAGE_SIZE_OPTIONS } from "../hooks/useClientPagination";
import { Button } from "./ui";

type PaginationBarProps = {
	page: number;
	totalPages: number;
	total: number;
	pageSize: number;
	rangeStart: number;
	rangeEnd: number;
	hasPrev: boolean;
	hasNext: boolean;
	onPageChange: (page: number) => void;
	onPageSizeChange: (size: number) => void;
	/** Compact single-line bar for dense ops tables. */
	compact?: boolean;
};

/**
 * Shared list footer: range summary, page size, prev/next.
 */
export function PaginationBar({
	page,
	totalPages,
	total,
	pageSize,
	rangeStart,
	rangeEnd,
	hasPrev,
	hasNext,
	onPageChange,
	onPageSizeChange,
	compact = true,
}: PaginationBarProps) {
	const { t } = useI18n();
	if (total === 0) return null;

	return (
		<div className={`pagination-bar${compact ? " is-compact" : ""}`}>
			<p className="pagination-summary">
				{t("pagination.range", {
					start: rangeStart,
					end: rangeEnd,
					total,
				})}
			</p>
			<div className="pagination-controls">
				<label className="pagination-size">
					<span className="sr-only">{t("pagination.pageSize")}</span>
					<select
						value={pageSize}
						aria-label={t("pagination.pageSize")}
						onChange={(event) => onPageSizeChange(Number(event.target.value))}
					>
						{PAGE_SIZE_OPTIONS.map((size) => (
							<option key={size} value={size}>
								{t("pagination.perPage", { n: size })}
							</option>
						))}
					</select>
				</label>
				<div className="pagination-pages">
					<Button
						variant="quiet"
						className="pagination-nav"
						disabled={!hasPrev}
						aria-label={t("pagination.prev")}
						onClick={() => onPageChange(page - 1)}
					>
						<ChevronLeft size={16} />
					</Button>
					<span className="pagination-page-label">
						{t("pagination.pageOf", { page, total: totalPages })}
					</span>
					<Button
						variant="quiet"
						className="pagination-nav"
						disabled={!hasNext}
						aria-label={t("pagination.next")}
						onClick={() => onPageChange(page + 1)}
					>
						<ChevronRight size={16} />
					</Button>
				</div>
			</div>
		</div>
	);
}
