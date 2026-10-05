import type { ReactNode } from "react";
import { Search } from "lucide-react";

/** Shared directory search layout. Filters remain scoped to each account. */
export function ModelDirectoryToolbar({ value, onChange, label, children }: {
  value: string;
  onChange: (value: string) => void;
  label: string;
  children?: ReactNode;
}) {
  return <div className="models-simple-toolbar">
    <label className="directory-search models-search">
      <Search size={14} aria-hidden="true" />
      <input value={value} onChange={(event) => onChange(event.target.value)}
        placeholder={label} aria-label={label} />
    </label>
    {children}
  </div>;
}
