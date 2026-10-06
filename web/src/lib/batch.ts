/** Bounded execution with per-item failures retained for selective retry. */
export async function runBatch<T>(
  items: readonly T[],
  action: (item: T) => Promise<unknown>,
  concurrency = 4,
) {
  let cursor = 0;
  const failures: { item: T; error: unknown }[] = [];
  await Promise.all(
    Array.from({ length: Math.min(items.length, Math.max(1, concurrency)) }, async () => {
      while (cursor < items.length) {
        const item = items[cursor++]!;
        try {
          await action(item);
        } catch (error) {
          failures.push({ item, error });
        }
      }
    }),
  );
  return { ok: items.length - failures.length, total: items.length, failures };
}
