/**
 * How many model-page filters are narrowing the list right now.
 *
 * `status` counts like any other filter — "all" is only the baseline the list
 * starts from, so picking anything else is a real narrowing. Search is
 * deliberately excluded: that box is always on screen with its text visible,
 * so it can never be the hidden state this exists to expose.
 *
 * This is also the filter panel's default-open rule (open ⟺ count > 0). The
 * three selects remember themselves in sessionStorage across navigation while
 * living behind a disclosure, so a restored filter used to shorten the list
 * with nothing on screen saying so — the model just looked missing. Tying the
 * panel to this same number keeps the two from disagreeing, and closes the
 * panel again once the filters are cleared.
 */
export type ModelFilterState = {
  group: string;
  channel: number;
  status: string;
};

export function countActiveModelFilters(filters: ModelFilterState) {
  return (
    (filters.group !== "" ? 1 : 0) +
    (filters.channel !== 0 ? 1 : 0) +
    (filters.status !== "all" ? 1 : 0)
  );
}
