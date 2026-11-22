import { describe, expect, it } from 'vitest';
import {
  type ClothingConditionState,
  type DailyWeatherManifest,
  type DaypartSlot,
  type SchoolDayThresholds,
  resolveBirthdayOverride,
  resolveBirthdayPackSpriteUrl,
  resolveCharacterPackSpriteUrl,
  resolveClothingConditionState,
  resolveDailyManifestSpriteUrl,
  resolveUSHolidaySpriteUrl,
  selectDailyVariantIndex,
} from './schoolDayPack';

describe('schoolDayPack utilities', () => {
  describe('resolveClothingConditionState', () => {
    const custom: SchoolDayThresholds = { rainThresholdPct: 20, coldMaxF: 40, coolMaxF: 55, warmMaxF: 70 };
    const tests: {
      name: string;
      tempF: number;
      precip: number;
      icon: string;
      slot: DaypartSlot;
      thresholds?: SchoolDayThresholds;
      want: ClothingConditionState;
    }[] = [
      { name: 'precip >= threshold: morning partly cloudy', tempF: 60, precip: 35, icon: 'weather-partly-cloudy', slot: 'morning', want: 'morning_rain' },
      { name: 'precip >= threshold: morning sunny', tempF: 70, precip: 80, icon: 'weather-sunny', slot: 'morning', want: 'morning_rain' },
      { name: 'precip >= threshold: afternoon partly cloudy', tempF: 60, precip: 35, icon: 'weather-partly-cloudy', slot: 'afternoon', want: 'rainy_boots' },
      { name: 'precip >= threshold: afternoon sunny', tempF: 80, precip: 50, icon: 'weather-sunny', slot: 'afternoon', want: 'rainy_boots' },
      { name: 'precip >= threshold: evening', tempF: 55, precip: 40, icon: 'weather-partly-cloudy', slot: 'evening', want: 'rainy_boots' },
      { name: 'rain icon: morning rainy', tempF: 65, precip: 0, icon: 'weather-rainy', slot: 'morning', want: 'morning_rain' },
      { name: 'rain icon: morning pouring', tempF: 65, precip: 10, icon: 'weather-pouring', slot: 'morning', want: 'morning_rain' },
      { name: 'rain icon: morning lightning', tempF: 65, precip: 5, icon: 'weather-lightning', slot: 'morning', want: 'morning_rain' },
      { name: 'rain icon: morning lightning-rainy', tempF: 65, precip: 0, icon: 'weather-lightning-rainy', slot: 'morning', want: 'morning_rain' },
      { name: 'rain icon: afternoon rainy', tempF: 65, precip: 0, icon: 'weather-rainy', slot: 'afternoon', want: 'rainy_boots' },
      { name: 'rain icon: evening pouring', tempF: 65, precip: 0, icon: 'weather-pouring', slot: 'evening', want: 'rainy_boots' },
      { name: 'rain icon: afternoon lightning', tempF: 65, precip: 0, icon: 'weather-lightning', slot: 'afternoon', want: 'rainy_boots' },
      { name: 'cold: morning 49F', tempF: 49, precip: 10, icon: 'weather-sunny', slot: 'morning', want: 'cold_puffer' },
      { name: 'cold: afternoon 32F', tempF: 32, precip: 0, icon: 'weather-cloudy', slot: 'afternoon', want: 'cold_puffer' },
      { name: 'cold: evening 45F', tempF: 45, precip: 20, icon: 'weather-partly-cloudy', slot: 'evening', want: 'cold_puffer' },
      { name: 'morning mild: 50F', tempF: 50, precip: 0, icon: 'weather-sunny', slot: 'morning', want: 'morning_mild' },
      { name: 'morning mild: 64F', tempF: 64, precip: 10, icon: 'weather-sunny', slot: 'morning', want: 'morning_mild' },
      { name: 'morning mild: 75F', tempF: 75, precip: 20, icon: 'weather-sunny', slot: 'morning', want: 'morning_mild' },
      { name: 'morning mild: 90F', tempF: 90, precip: 0, icon: 'weather-sunny', slot: 'morning', want: 'morning_mild' },
      { name: 'cool: afternoon 50F', tempF: 50, precip: 0, icon: 'weather-sunny', slot: 'afternoon', want: 'cool_evening' },
      { name: 'cool: afternoon 64F', tempF: 64, precip: 15, icon: 'weather-sunny', slot: 'afternoon', want: 'cool_evening' },
      { name: 'cool: evening 58F', tempF: 58, precip: 0, icon: 'weather-partly-cloudy', slot: 'evening', want: 'cool_evening' },
      { name: 'warm: afternoon 65F', tempF: 65, precip: 0, icon: 'weather-sunny', slot: 'afternoon', want: 'warm_pants' },
      { name: 'warm: afternoon 77F', tempF: 77, precip: 20, icon: 'weather-sunny', slot: 'afternoon', want: 'warm_pants' },
      { name: 'warm: evening 70F', tempF: 70, precip: 0, icon: 'weather-partly-cloudy', slot: 'evening', want: 'warm_pants' },
      { name: 'hot: afternoon 78F', tempF: 78, precip: 0, icon: 'weather-sunny', slot: 'afternoon', want: 'hot_dress' },
      { name: 'hot: afternoon 95F', tempF: 95, precip: 10, icon: 'weather-sunny', slot: 'afternoon', want: 'hot_dress' },
      { name: 'hot: evening clear night 82F', tempF: 82, precip: 0, icon: 'weather-clear-night', slot: 'evening', want: 'hot_dress' },
      { name: 'custom thresholds: rain at 22%', tempF: 60, precip: 22, icon: 'weather-cloudy', slot: 'morning', thresholds: custom, want: 'morning_rain' },
      { name: 'custom thresholds: cold at 40F', tempF: 40, precip: 10, icon: 'weather-sunny', slot: 'afternoon', thresholds: custom, want: 'cold_puffer' },
      { name: 'custom thresholds: cool at 52F', tempF: 52, precip: 0, icon: 'weather-sunny', slot: 'afternoon', thresholds: custom, want: 'cool_evening' },
      { name: 'custom thresholds: warm at 68F', tempF: 68, precip: 0, icon: 'weather-sunny', slot: 'afternoon', thresholds: custom, want: 'warm_pants' },
      { name: 'custom thresholds: hot at 72F', tempF: 72, precip: 0, icon: 'weather-sunny', slot: 'afternoon', thresholds: custom, want: 'hot_dress' },
      { name: 'default thresholds: cold edge 49F', tempF: 49, precip: 0, icon: 'weather-sunny', slot: 'morning', want: 'cold_puffer' },
      { name: 'default thresholds: mild edge 50F', tempF: 50, precip: 0, icon: 'weather-sunny', slot: 'morning', want: 'morning_mild' },
      { name: 'default thresholds: cool edge 64F', tempF: 64, precip: 0, icon: 'weather-sunny', slot: 'afternoon', want: 'cool_evening' },
      { name: 'default thresholds: warm edge 77F', tempF: 77, precip: 0, icon: 'weather-sunny', slot: 'afternoon', want: 'warm_pants' },
      { name: 'default thresholds: hot edge 78F', tempF: 78, precip: 0, icon: 'weather-sunny', slot: 'afternoon', want: 'hot_dress' },
    ];
    for (const tc of tests) {
      it(tc.name, () => {
        expect(resolveClothingConditionState(tc.tempF, tc.precip, tc.icon, tc.slot, tc.thresholds)).toBe(tc.want);
      });
    }
  });

  describe('selectDailyVariantIndex', () => {
    // Same-date slots are consecutive (mod count), so counts >= 3 give distinct morning/afternoon/evening variants.
    const tests: { name: string; date: string; slot: DaypartSlot; count: number; want: number }[] = [
      { name: 'morning 2026-10-31', date: '2026-10-31', slot: 'morning', count: 5, want: 4 },
      { name: 'afternoon 2026-10-31', date: '2026-10-31', slot: 'afternoon', count: 5, want: 5 },
      { name: 'evening 2026-10-31 wraps to 1', date: '2026-10-31', slot: 'evening', count: 5, want: 1 },
      { name: 'count 3: 2026-11-00', date: '2026-11-00', slot: 'morning', count: 3, want: 1 },
      { name: 'count 3: 2026-11-01', date: '2026-11-01', slot: 'morning', count: 3, want: 2 },
      { name: 'count 3: 2026-11-02', date: '2026-11-02', slot: 'morning', count: 3, want: 3 },
      { name: 'count 3: 2026-11-03', date: '2026-11-03', slot: 'morning', count: 3, want: 1 },
      { name: 'count 3: 2026-11-04', date: '2026-11-04', slot: 'morning', count: 3, want: 2 },
      { name: 'count 3: 2026-11-05', date: '2026-11-05', slot: 'morning', count: 3, want: 3 },
      { name: 'count 3: 2026-11-06', date: '2026-11-06', slot: 'morning', count: 3, want: 1 },
      { name: 'count 3: 2026-11-07', date: '2026-11-07', slot: 'morning', count: 3, want: 2 },
      { name: 'count 3: 2026-11-08', date: '2026-11-08', slot: 'morning', count: 3, want: 3 },
      { name: 'count 3: 2026-11-09', date: '2026-11-09', slot: 'morning', count: 3, want: 1 },
      { name: 'count 0 clamps to 1', date: '2026-10-31', slot: 'morning', count: 0, want: 1 },
      { name: 'negative count clamps to 1', date: '2026-10-31', slot: 'morning', count: -5, want: 1 },
      { name: 'distinct slots 2026-11-07: morning', date: '2026-11-07', slot: 'morning', count: 5, want: 3 },
      { name: 'distinct slots 2026-11-07: afternoon', date: '2026-11-07', slot: 'afternoon', count: 5, want: 4 },
      { name: 'distinct slots 2026-11-07: evening', date: '2026-11-07', slot: 'evening', count: 5, want: 5 },
      { name: 'distinct slots 2026-11-08: morning', date: '2026-11-08', slot: 'morning', count: 5, want: 4 },
      { name: 'distinct slots 2026-11-08: afternoon', date: '2026-11-08', slot: 'afternoon', count: 5, want: 5 },
      { name: 'distinct slots 2026-11-08: evening', date: '2026-11-08', slot: 'evening', count: 5, want: 1 },
      { name: 'distinct slots 2026-12-25: morning', date: '2026-12-25', slot: 'morning', count: 5, want: 4 },
      { name: 'distinct slots 2026-12-25: afternoon', date: '2026-12-25', slot: 'afternoon', count: 5, want: 5 },
      { name: 'distinct slots 2026-12-25: evening', date: '2026-12-25', slot: 'evening', count: 5, want: 1 },
      { name: 'distinct slots 2026-01-01: morning', date: '2026-01-01', slot: 'morning', count: 5, want: 1 },
      { name: 'distinct slots 2026-01-01: afternoon', date: '2026-01-01', slot: 'afternoon', count: 5, want: 2 },
      { name: 'distinct slots 2026-01-01: evening', date: '2026-01-01', slot: 'evening', count: 5, want: 3 },
    ];
    for (const tc of tests) {
      it(tc.name, () => {
        expect(selectDailyVariantIndex(tc.date, tc.slot, tc.count)).toBe(tc.want);
      });
    }
  });

  describe('resolveBirthdayOverride and resolveBirthdayPackSpriteUrl', () => {
    it('resolves birthday override from array and object configurations using generic fixtures', () => {
      const arrayConfig = [
        { name: 'ChildOne', date: '11-07' },
        { key: 'child_two', name: 'ChildTwo', date: '05-14', banner_label: 'Party Time!' },
      ];
      const match1 = resolveBirthdayOverride('2026-11-07', arrayConfig);
      expect(match1).toEqual({
        key: 'childone',
        name: 'ChildOne',
        bannerLabel: '🎂 Happy Birthday ChildOne!',
        outfitLabel: '🎂 Birthday Celebration!',
      });

      const match2 = resolveBirthdayOverride('2026-05-14', arrayConfig);
      expect(match2).toEqual({
        key: 'child_two',
        name: 'ChildTwo',
        bannerLabel: 'Party Time!',
        outfitLabel: '🎂 Birthday Celebration!',
      });

      expect(resolveBirthdayOverride('2026-11-08', arrayConfig)).toBeUndefined();

      const mapConfig = {
        ChildOne: '11-07',
      };
      expect(resolveBirthdayOverride('2026-11-07', mapConfig)).toEqual({
        key: 'childone',
        name: 'ChildOne',
        bannerLabel: '🎂 Happy Birthday ChildOne!',
        outfitLabel: '🎂 Birthday Celebration!',
      });
    });

    it('resolves numeric month/day entries, object-valued map entries, date-keyed string map entries, and custom overrides', () => {
      // Numeric { month, day, name } entries and custom id / label / outfit_label overrides
      const numericAndCustomArray = [
        { month: 11, day: 7, name: 'ChildOne' },
        {
          id: 'custom_id_two',
          month_day: '05/14',
          label: 'Custom Banner Label!',
          outfit_label: 'Party Hat & Confetti!',
        },
      ];
      expect(resolveBirthdayOverride('2026-11-07', numericAndCustomArray)).toEqual({
        key: 'childone',
        name: 'ChildOne',
        bannerLabel: '🎂 Happy Birthday ChildOne!',
        outfitLabel: '🎂 Birthday Celebration!',
      });
      expect(resolveBirthdayOverride('2026-05-14', numericAndCustomArray)).toEqual({
        key: 'custom_id_two',
        name: 'custom_id_two',
        bannerLabel: 'Custom Banner Label!',
        outfitLabel: 'Party Hat & Confetti!',
      });

      // Object-valued map entry keyed by name: { ChildOne: { date: '11-07' } }
      expect(
        resolveBirthdayOverride('2026-11-07', {
          ChildOne: { date: '11-07', outfit_label: 'Birthday Crown!' },
        }),
      ).toEqual({
        key: 'childone',
        name: 'ChildOne',
        bannerLabel: '🎂 Happy Birthday ChildOne!',
        outfitLabel: 'Birthday Crown!',
      });

      // Object-valued map entry keyed by date: { '11-07': { name: 'ChildOne' } }
      expect(
        resolveBirthdayOverride('2026-11-07', {
          '11-07': { name: 'ChildOne', label: 'Happy Day ChildOne!' },
        }),
      ).toEqual({
        key: 'childone',
        name: 'ChildOne',
        bannerLabel: 'Happy Day ChildOne!',
        outfitLabel: '🎂 Birthday Celebration!',
      });

      // Date-keyed string map entry: { '11-07': 'ChildOne' }
      expect(
        resolveBirthdayOverride('2026-11-07', {
          '11-07': 'ChildOne',
        }),
      ).toEqual({
        key: 'childone',
        name: 'ChildOne',
        bannerLabel: '🎂 Happy Birthday ChildOne!',
        outfitLabel: '🎂 Birthday Celebration!',
      });
    });

    it('returns undefined for invalid, boolean, or empty birthday configurations', () => {
      expect(resolveBirthdayOverride(undefined, { ChildOne: '11-07' })).toBeUndefined();
      expect(resolveBirthdayOverride('', { ChildOne: '11-07' })).toBeUndefined();
      expect(resolveBirthdayOverride('invalid-date', { ChildOne: '11-07' })).toBeUndefined();
      expect(resolveBirthdayOverride('2026-11-07', undefined)).toBeUndefined();
      expect(resolveBirthdayOverride('2026-11-07', null)).toBeUndefined();
      expect(resolveBirthdayOverride('2026-11-07', true)).toBeUndefined();
      expect(resolveBirthdayOverride('2026-11-07', false)).toBeUndefined();
      expect(resolveBirthdayOverride('2026-11-07', [])).toBeUndefined();
      expect(resolveBirthdayOverride('2026-11-07', {})).toBeUndefined();
      expect(
        resolveBirthdayOverride('2026-11-07', [null, 'invalid', [], { date: '11-07', name: '___' }]),
      ).toBeUndefined();
    });

    it('formats birthday pack sprite URLs cleanly', () => {
      expect(resolveBirthdayPackSpriteUrl('ghibli_school_day', 'childone', 'morning')).toBe(
        '/static/ui/assets/weather-packs/ghibli_school_day/birthday_childone_morning.jpg',
      );
      expect(
        resolveBirthdayPackSpriteUrl('custom_pack', 'childone', 'evening', '/custom/base'),
      ).toBe('/custom/base/custom_pack/birthday_childone_evening.jpg');
    });
  });

  describe('resolveCharacterPackSpriteUrl', () => {
    it('formats sprite URL with default assetsBase', () => {
      const url = resolveCharacterPackSpriteUrl('ghibli_school_day', 'cold_puffer', 3);
      expect(url).toBe('/static/ui/assets/weather-packs/ghibli_school_day/cold_puffer_v3.jpg');
    });

    it('formats sprite URL with custom assetsBase', () => {
      const url = resolveCharacterPackSpriteUrl(
        'custom_pack',
        'morning_rain',
        1,
        '/custom/assets/packs',
      );
      expect(url).toBe('/custom/assets/packs/custom_pack/morning_rain_v1.jpg');
    });
  });

  it('resolves holiday sprite when enableHolidays is true and state is supported', () => {
    expect(
      resolveCharacterPackSpriteUrl('ghibli', 'cold_puffer', 2, '2026-10-31', true),
    ).toBe('/static/ui/assets/weather-packs/ghibli/holiday_halloween_cold_puffer.jpg');
    expect(
      resolveCharacterPackSpriteUrl('ghibli', 'cold_puffer', 2, '2026-12-25', true),
    ).toBe('/static/ui/assets/weather-packs/ghibli/holiday_christmas_cold_puffer.jpg');
    expect(
      resolveCharacterPackSpriteUrl('ghibli', 'cold_puffer', 2, '2026-01-01', true),
    ).toBe('/static/ui/assets/weather-packs/ghibli/holiday_new_years_cold_puffer.jpg');
    expect(
      resolveCharacterPackSpriteUrl('ghibli', 'warm_pants', 2, '2026-02-14', true),
    ).toBe('/static/ui/assets/weather-packs/ghibli/holiday_valentines_warm_pants.jpg');
    expect(
      resolveCharacterPackSpriteUrl('ghibli', 'hot_dress', 2, '2026-07-04', true),
    ).toBe('/static/ui/assets/weather-packs/ghibli/holiday_july_4th_hot_dress.jpg');
    expect(
      resolveCharacterPackSpriteUrl('ghibli', 'warm_pants', 2, '2026-04-05', true),
    ).toBe('/static/ui/assets/weather-packs/ghibli/holiday_easter_warm_pants.jpg');
    expect(
      resolveCharacterPackSpriteUrl('ghibli', 'warm_pants', 2, '2026-05-25', true),
    ).toBe('/static/ui/assets/weather-packs/ghibli/holiday_memorial_day_warm_pants.jpg');
    expect(
      resolveCharacterPackSpriteUrl('ghibli', 'warm_pants', 2, '2026-09-07', true),
    ).toBe('/static/ui/assets/weather-packs/ghibli/holiday_labor_day_warm_pants.jpg');
    expect(
      resolveCharacterPackSpriteUrl('ghibli', 'cold_puffer', 2, '2026-11-26', true),
    ).toBe('/static/ui/assets/weather-packs/ghibli/holiday_thanksgiving_cold_puffer.jpg');
    // Unsupported state on holiday falls back to regular daily variant
    expect(
      resolveCharacterPackSpriteUrl('ghibli', 'hot_dress', 3, '2026-12-25', true),
    ).toBe('/static/ui/assets/weather-packs/ghibli/hot_dress_v3.jpg');
    // Non-holiday date falls back to regular daily variant
    expect(
      resolveCharacterPackSpriteUrl('ghibli', 'cold_puffer', 1, '2026-10-15', true),
    ).toBe('/static/ui/assets/weather-packs/ghibli/cold_puffer_v1.jpg');
  });

  describe('resolveUSHolidaySpriteUrl', () => {
    const tests: { name: string; state: ClothingConditionState; date: string | undefined; base?: string; want: string | undefined }[] = [
      { name: 'holiday with supported state', state: 'hot_dress', date: '2026-07-04', want: '/static/ui/assets/weather-packs/ghibli/holiday_july_4th_hot_dress.jpg' },
      { name: 'holiday with unsupported state', state: 'hot_dress', date: '2026-12-25', want: undefined },
      { name: 'not a US holiday', state: 'cold_puffer', date: '2026-10-15', want: undefined },
      { name: 'undefined date', state: 'cold_puffer', date: undefined, want: undefined },
      { name: 'custom assetsBase', state: 'hot_dress', date: '2026-07-04', base: '/custom/base', want: '/custom/base/ghibli/holiday_july_4th_hot_dress.jpg' },
    ];
    for (const tc of tests) {
      it(tc.name, () => {
        expect(resolveUSHolidaySpriteUrl('ghibli', tc.state, tc.date, tc.base)).toBe(tc.want);
      });
    }
  });

  describe('resolveDailyManifestSpriteUrl', () => {
    const valid: DailyWeatherManifest = {
      target_date: '2026-11-17',
      theme_title: 'Studio Ghibli Autumn',
      style_label: 'Watercolor Storybook',
      slots: {
        morning: 'daily_2026-11-17_morning.jpg',
        afternoon: './daily_2026-11-17_afternoon.jpg',
        evening: '/custom/path/evening.jpg',
      },
    };
    const remote: DailyWeatherManifest = {
      target_date: '2026-11-17',
      slots: { morning: 'http://example.com/morning.jpg', afternoon: 'https://cdn.example.com/afternoon.png' },
    };
    const traversal: DailyWeatherManifest = {
      target_date: '2026-11-17',
      slots: { morning: '../escape/secret.jpg', afternoon: 'sub/../../secret.jpg', evening: '..' },
    };
    const sparse: DailyWeatherManifest = { target_date: '2026-11-17', slots: { morning: '   ' } };
    const tests: {
      name: string;
      manifest: DailyWeatherManifest | undefined | null;
      date: string | undefined;
      slot: DaypartSlot;
      base?: string;
      want: string | undefined;
    }[] = [
      { name: 'relative filename under assetsBase/pack', manifest: valid, date: '2026-11-17', slot: 'morning', want: '/static/ui/assets/weather-packs/ghibli_pack/daily_2026-11-17_morning.jpg' },
      { name: 'strips leading ./', manifest: valid, date: '2026-11-17', slot: 'afternoon', want: '/static/ui/assets/weather-packs/ghibli_pack/daily_2026-11-17_afternoon.jpg' },
      { name: 'absolute path passes through', manifest: valid, date: '2026-11-17', slot: 'evening', want: '/custom/path/evening.jpg' },
      { name: 'http URL passes through', manifest: remote, date: '2026-11-17', slot: 'morning', want: 'http://example.com/morning.jpg' },
      { name: 'https URL passes through', manifest: remote, date: '2026-11-17', slot: 'afternoon', want: 'https://cdn.example.com/afternoon.png' },
      { name: 'custom assetsBase', manifest: valid, date: '2026-11-17', slot: 'morning', base: '/custom/assets', want: '/custom/assets/ghibli_pack/daily_2026-11-17_morning.jpg' },
      { name: 'stale manifest target_date', manifest: valid, date: '2026-11-18', slot: 'morning', want: undefined },
      { name: 'rejects leading .. traversal', manifest: traversal, date: '2026-11-17', slot: 'morning', want: undefined },
      { name: 'rejects nested .. traversal', manifest: traversal, date: '2026-11-17', slot: 'afternoon', want: undefined },
      { name: 'rejects bare ..', manifest: traversal, date: '2026-11-17', slot: 'evening', want: undefined },
      { name: 'whitespace-only slot', manifest: sparse, date: '2026-11-17', slot: 'morning', want: undefined },
      { name: 'missing slot', manifest: sparse, date: '2026-11-17', slot: 'afternoon', want: undefined },
      { name: 'undefined manifest', manifest: undefined, date: '2026-11-17', slot: 'morning', want: undefined },
      { name: 'null manifest', manifest: null, date: '2026-11-17', slot: 'morning', want: undefined },
      { name: 'manifest without target_date', manifest: {}, date: '2026-11-17', slot: 'morning', want: undefined },
      { name: 'undefined dateStr', manifest: valid, date: undefined, slot: 'morning', want: undefined },
    ];
    for (const tc of tests) {
      it(tc.name, () => {
        expect(resolveDailyManifestSpriteUrl('ghibli_pack', tc.manifest, tc.date, tc.slot, tc.base)).toBe(tc.want);
      });
    }
  });
});
