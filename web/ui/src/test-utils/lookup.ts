/**
 * Test-only lookups that narrow away the `| undefined` added by noUncheckedIndexedAccess.
 * A missing collection or element fails the test with a message naming the index or key,
 * instead of a TypeError further down the assertion chain.
 */

/** Element `index` of an array, NodeList, mock.calls or other array-like. */
export function itemAt<T>(items: ArrayLike<T> | null | undefined, index: number): T {
  if (!items) throw new Error(`expected a collection to read index ${index} from, got ${String(items)}`);
  const item = items[index];
  if (item === undefined) {
    throw new Error(`expected an item at index ${index}, but length is ${items.length}`);
  }
  return item;
}

/** Value at `key` of a string-keyed record. */
export function entryAt<T>(record: Readonly<Partial<Record<string, T>>>, key: string): T {
  const value = record[key];
  if (value === undefined) throw new Error(`expected an entry for key "${key}"`);
  return value;
}
