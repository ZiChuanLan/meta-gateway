import type { UpdateCheckStatus } from "../api/types";

/** A release as [major, minor, patch, betaN]; betaN is Infinity for a final
 *  release so it sorts above every prerelease of the same core. */
function parseVersion(raw: string): number[] | null {
  const match = /^v?(\d+)\.(\d+)\.(\d+)(?:-beta\.(\d+))?$/.exec(raw);
  return match
    ? [
        Number(match[1]),
        Number(match[2]),
        Number(match[3]),
        match[4] === undefined ? Infinity : Number(match[4]),
      ]
    : null;
}

/**
 * Dotted-numeric release comparison: 1 when `a` is newer, -1 when older, 0 when
 * equal. Returns null when either side is not a comparable release (a "dev"
 * build, a custom tag), which is a different answer from "equal".
 *
 * Shared by the settings panel, the dialog and the update watch. The watch needs
 * it because the watchtower executor installs whatever its floating tag points
 * to, so the build that comes up has to be compared with the one being replaced
 * rather than matched against the version the console named.
 */
export function compareVersions(a: string, b: string): number | null {
  const left = parseVersion(a);
  const right = parseVersion(b);
  if (!left || !right) return null;
  for (let i = 0; i < left.length; i++) {
    if (left[i]! > right[i]!) return 1;
    if (left[i]! < right[i]!) return -1;
  }
  return 0;
}

export function updateState(status: UpdateCheckStatus) {
  if (!status.enabled) return "disabled";
  if (status.error) return "checkFailed";
  if (!status.latest) return "unchecked";
  const cmp = compareVersions(status.current, status.latest);
  if (cmp === null) return "uncomparable";
  if (cmp > 0) return "ahead";
  if (cmp < 0) return "available";
  return "current";
}

/** What an observed update is being measured against. */
export interface UpdateWatch {
  /** The release the console confirmed. */
  target: string;
  startedAt: number;
  /** The build running when the update was submitted. */
  from?: string;
  /** The floating tag the executor updates, present only in watchtower mode. */
  tracked?: string;
}

/** Has the update landed?
 *
 * The socket path pulls one exact release, so the version has to match it. The
 * watchtower path cannot promise that: its executor installs whatever the
 * tracked floating tag points to at the moment it runs, which is how a console
 * that offered v4.0.0-beta.5 installed v4.0.0-beta.6 and then waited sixteen
 * minutes for a version that was never going to appear. There, "the build
 * changed to something newer than the one we left behind" is the only claim the
 * console is entitled to make.
 */
export function updateLanded(
  watch: Pick<UpdateWatch, "target" | "from" | "tracked">,
  version: string | undefined,
): boolean {
  if (!version) return false;
  // The named release is confirmation on either path, and it is all the console
  // has to go on when a resuming observer has no starting version.
  if (version === watch.target) return true;
  if (!watch.tracked) return false;
  if (!watch.from || version === watch.from) return false;
  return compareVersions(version, watch.from) === 1;
}

/** The tracked tag came back with an older build than the one being replaced. */
export function updateWentBackwards(
  watch: Pick<UpdateWatch, "from" | "tracked">,
  version: string | undefined,
): boolean {
  if (!watch.tracked || !watch.from || !version || version === watch.from) return false;
  return compareVersions(version, watch.from) === -1;
}
