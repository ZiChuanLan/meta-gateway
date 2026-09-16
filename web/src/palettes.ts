/** Preset colour palettes.
 *
 * Only the identity lives here; every colour is declared in
 * `styles/palettes.css`, which is the single source of truth for the swatches
 * and the tokens alike. */
export type PaletteId = "default" | "ocean" | "forest" | "violet" | "ember" | "graphite" | "sakura";

export const PALETTES: { id: PaletteId; nameKey: string }[] = [
  { id: "default", nameKey: "appearance.palette.default" },
  { id: "ocean", nameKey: "appearance.palette.ocean" },
  { id: "forest", nameKey: "appearance.palette.forest" },
  { id: "violet", nameKey: "appearance.palette.violet" },
  { id: "ember", nameKey: "appearance.palette.ember" },
  { id: "graphite", nameKey: "appearance.palette.graphite" },
  { id: "sakura", nameKey: "appearance.palette.sakura" },
];

export const PALETTE_IDS = PALETTES.map((palette) => palette.id);

/** Unknown or absent values fall back to the appearance's own brand colours. */
export function normalizePalette(value: string | null): PaletteId {
  return PALETTE_IDS.find((id) => id === value) ?? "default";
}
