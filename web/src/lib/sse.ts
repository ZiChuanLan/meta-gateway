/**
 * Minimal SSE reader. The console streams two things it owns — the live trace
 * and the workbench probe — and both need the same two behaviours: split only
 * on complete frame boundaries (a chunk can end mid-frame), and keep named
 * events distinguishable from the payload frames an upstream relays verbatim.
 */
export type SseFrame = { event: string; data: string };

export function splitSseFrames(buffer: string): { frames: SseFrame[]; rest: string } {
  const blocks = buffer.split("\n\n");
  const rest = blocks.pop() ?? "";
  return { frames: blocks.map(parseSseFrame).filter((frame) => frame !== null), rest };
}

export function parseSseFrame(block: string): SseFrame | null {
  const data: string[] = [];
  let event = "";
  let sawField = false;
  for (const line of block.split("\n")) {
    if (line.startsWith(":")) continue; // comment / keep-alive
    if (line.startsWith("event:")) {
      event = line.slice(6).trim();
      sawField = true;
      continue;
    }
    if (line.startsWith("data:")) {
      // A single optional space after the colon is part of the framing, not the
      // payload — stripping it keeps JSON.parse working on relayed frames.
      data.push(line.slice(5).replace(/^ /, ""));
      sawField = true;
    }
  }
  if (!sawField) return null;
  return { event, data: data.join("\n") };
}

/** Parses a frame payload that is expected to be JSON, without throwing. */
export function parseSseJson<T = unknown>(data: string): T | null {
  const trimmed = data.trim();
  if (!trimmed || trimmed === "[DONE]") return null;
  try {
    return JSON.parse(trimmed) as T;
  } catch {
    return null;
  }
}
