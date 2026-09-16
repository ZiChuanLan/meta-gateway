import { useUITheme } from "../appearance";
import { UI_THEMES } from "./registry";
import type { ThemeDetailsProps } from "./types";

export function ThemeDetails(props: ThemeDetailsProps) {
  const Details = UI_THEMES[useUITheme()].Details;
  return <Details {...props} />;
}
