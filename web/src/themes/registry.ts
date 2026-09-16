import { ClassicChrome } from "./classic/Chrome";
import { ClassicEntrance } from "./classic/Entrance";
import { ModernChrome } from "./modern/Chrome";
import { ModernEntrance } from "./modern/Entrance";
import { ClassicDetails } from "./classic/Details";
import { ModernDetails } from "./modern/Details";
import type { UIThemeId, UIThemePackage } from "./types";

export const UI_THEMES: Record<UIThemeId, UIThemePackage> = {
  classic: { id: "classic", version: "1.0.0", apiVersion: 1, nameKey: "appearance.classic.name", descriptionKey: "appearance.classic.description", Chrome: ClassicChrome, Entrance: ClassicEntrance, Details: ClassicDetails, channelDetails: "split" },
  modern: { id: "modern", version: "1.0.0", apiVersion: 1, nameKey: "appearance.modern.name", descriptionKey: "appearance.modern.description", Chrome: ModernChrome, Entrance: ModernEntrance, Details: ModernDetails, channelDetails: "drawer" },
};
