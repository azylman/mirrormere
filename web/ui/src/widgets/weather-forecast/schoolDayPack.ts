export interface SchoolDayThresholds {
  rainThresholdPct: number; // default: 35
  coldMaxF: number;         // default: 49 (< 50F -> cold puffer coat)
  coolMaxF: number;         // default: 64 (50-64F -> hoodie/fleece + long pants)
  warmMaxF: number;         // default: 77 (65-77F -> t-shirt + long pants; >= 78F -> summer dress / hot weather outfit)
}

export const DEFAULT_SCHOOL_DAY_THRESHOLDS: SchoolDayThresholds = {
  rainThresholdPct: 35,
  coldMaxF: 49,
  coolMaxF: 64,
  warmMaxF: 77,
};

export type DaypartSlot = 'morning' | 'afternoon' | 'evening';

export type ClothingConditionState =
  | 'morning_rain'
  | 'rainy_boots'
  | 'cold_puffer'
  | 'morning_mild'
  | 'cool_evening'
  | 'warm_pants'
  | 'hot_dress';

export function resolveClothingConditionState(
  tempF: number,
  precipProbPct: number,
  iconToken: string,
  slot: DaypartSlot,
  thresholds: SchoolDayThresholds = DEFAULT_SCHOOL_DAY_THRESHOLDS,
): ClothingConditionState {
  const isWetIcon =
    iconToken.includes('rain') ||
    iconToken.includes('pouring') ||
    iconToken.includes('lightning');
  if (precipProbPct >= thresholds.rainThresholdPct || isWetIcon) {
    return slot === 'morning' ? 'morning_rain' : 'rainy_boots';
  }
  if (tempF <= thresholds.coldMaxF) {
    return 'cold_puffer';
  }
  if (slot === 'morning') {
    return 'morning_mild';
  }
  if (tempF <= thresholds.coolMaxF) {
    return 'cool_evening';
  }
  if (tempF <= thresholds.warmMaxF) {
    return 'warm_pants';
  }
  return 'hot_dress';
}

export function selectDailyVariantIndex(
  dateStr: string,
  slot: DaypartSlot,
  variantsCount = 5,
): number {
  const count = Math.max(1, variantsCount);
  let hash = 0;
  const seed = `${dateStr}:${slot}`;
  for (let i = 0; i < seed.length; i++) {
    hash = (hash * 31 + seed.charCodeAt(i)) >>> 0;
  }
  return (hash % count) + 1;
}

export function resolveCharacterPackSpriteUrl(
  packName: string,
  state: ClothingConditionState,
  variantIndex: number,
  assetsBase = '/static/ui/assets/weather-packs',
): string {
  return `${assetsBase}/${packName}/${state}_v${variantIndex}.jpg`;
}
