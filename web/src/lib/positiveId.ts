/** Parses a query-string value into a positive integer id; anything else
 * (missing, non-numeric, zero, negative, fractional) yields undefined so
 * callers can omit the filter entirely. */
export function positiveId(value: string | number | null | undefined): number | undefined {
	if (value == null || value === "") return undefined;
	const parsed = Number(value);
	return Number.isInteger(parsed) && parsed > 0 ? parsed : undefined;
}
