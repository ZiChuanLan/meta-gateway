import type { ThemeDetailsProps } from "../types";

export function ClassicDetails({ title, children }: ThemeDetailsProps) {
  return <aside className="classic-channel-detail" aria-label={title}>{children}</aside>;
}
