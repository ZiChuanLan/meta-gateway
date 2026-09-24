import type { ComponentType, ReactNode } from "react";
import type { LucideIcon } from "lucide-react";
import type { UpdateCheckStatus } from "../api/types";

export type UIThemeId = "classic" | "modern";
export type ThemeNavItem = { to: string; label: string; icon: LucideIcon };
export type ThemeChromeProps = {
  navigation: ReactNode;
  sections: { label: string; items: ThemeNavItem[] }[];
  version: string;
  theme: "light" | "dark";
  onThemeChange: () => void;
  onSearch: () => void;
  onDisconnect: () => void;
  health: { healthy: number; total: number; loading: boolean };
  tone: "idle" | "ok" | "down" | "warn";
  update?: UpdateCheckStatus;
  collapsed: boolean;
  onCollapse: () => void;
  onOpenNav: () => void;
  sectionLabel: string;
  pageLabel: string;
};
export type EntrancePhase = "sealing" | "revealing" | "sheathing";
export type ThemeDetailsProps = { open: boolean; onClose: () => void; title: string; children: ReactNode };
export type UIThemePackage = {
  id: UIThemeId;
  version: string;
  apiVersion: 1;
  nameKey: string;
  descriptionKey: string;
  Chrome: ComponentType<ThemeChromeProps>;
  Entrance: ComponentType<{ phase: EntrancePhase }>;
  Details: ComponentType<ThemeDetailsProps>;
  channelDetails: "split" | "drawer";
};
