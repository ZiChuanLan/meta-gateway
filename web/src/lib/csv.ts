/**
 * Minimal CSV export for console tables. Values are quoted only when they
 * contain a delimiter, quote or newline so the output stays readable; a UTF-8
 * BOM is prepended because Excel otherwise mangles CJK columns.
 */

export type CsvValue = string | number | boolean | null | undefined;

function cell(value: CsvValue): string {
  if (value == null) return "";
  const text = String(value);
  if (!/[",\r\n]/.test(text)) return text;
  return `"${text.replace(/"/g, '""')}"`;
}

export function toCSV(rows: CsvValue[][]): string {
  return rows.map((row) => row.map(cell).join(",")).join("\r\n");
}

export function downloadText(filename: string, text: string, mime = "text/csv;charset=utf-8") {
  const blob = new Blob(["\uFEFF", text], { type: mime });
  const url = URL.createObjectURL(blob);
  const anchor = document.createElement("a");
  anchor.href = url;
  anchor.download = filename;
  document.body.appendChild(anchor);
  anchor.click();
  anchor.remove();
  // Revoking immediately can cancel the download in some browsers.
  window.setTimeout(() => URL.revokeObjectURL(url), 2000);
}

/** `meta-gateway-logs-20260917-2016.csv` — sortable, filesystem-safe. */
export function timestampedName(prefix: string, extension: string, at = new Date()): string {
  const pad = (n: number) => String(n).padStart(2, "0");
  const stamp = `${at.getFullYear()}${pad(at.getMonth() + 1)}${pad(at.getDate())}-${pad(at.getHours())}${pad(at.getMinutes())}${pad(at.getSeconds())}`;
  return `${prefix}-${stamp}.${extension}`;
}
