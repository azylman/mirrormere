import { describe, expect, it } from 'vitest';
import { entryAt, itemAt } from './lookup';

describe('itemAt', () => {
  const tests: { name: string; items: ArrayLike<string> | null | undefined; index: number; want?: string; wantError?: string }[] = [
    { name: 'first item', items: ['a', 'b'], index: 0, want: 'a' },
    { name: 'last item', items: ['a', 'b'], index: 1, want: 'b' },
    { name: 'array-like', items: { length: 1, 0: 'x' }, index: 0, want: 'x' },
    { name: 'past the end', items: ['a'], index: 1, wantError: 'expected an item at index 1, but length is 1' },
    { name: 'empty', items: [], index: 0, wantError: 'expected an item at index 0, but length is 0' },
    { name: 'missing collection', items: undefined, index: 2, wantError: 'expected a collection to read index 2 from, got undefined' },
    { name: 'null collection', items: null, index: 0, wantError: 'expected a collection to read index 0 from, got null' },
  ];
  for (const tc of tests) {
    it(tc.name, () => {
      if (tc.wantError) {
        expect(() => itemAt(tc.items, tc.index)).toThrow(tc.wantError);
      } else {
        expect(itemAt(tc.items, tc.index)).toBe(tc.want);
      }
    });
  }
});

describe('entryAt', () => {
  const tests: { name: string; record: Record<string, number | undefined>; key: string; want?: number; wantError?: string }[] = [
    { name: 'present key', record: { a: 1, b: 2 }, key: 'b', want: 2 },
    { name: 'falsy value is present', record: { zero: 0 }, key: 'zero', want: 0 },
    { name: 'absent key', record: { a: 1 }, key: 'z', wantError: 'expected an entry for key "z"' },
    { name: 'explicitly undefined', record: { a: undefined }, key: 'a', wantError: 'expected an entry for key "a"' },
  ];
  for (const tc of tests) {
    it(tc.name, () => {
      if (tc.wantError) {
        expect(() => entryAt(tc.record, tc.key)).toThrow(tc.wantError);
      } else {
        expect(entryAt(tc.record, tc.key)).toBe(tc.want);
      }
    });
  }
});
