# Custom Weather Character Packs

Mirrormere's `weather-forecast` widget supports optional user-supplied character illustration packs that change dynamically with the temperature, precipitation, and time of day.

## How to Configure

1. Create a folder for your pack under `web/ui/public/assets/weather-packs/<your_pack_name>/` (or volume-mount it into the container at `/app/web/static/ui/assets/weather-packs/<your_pack_name>/`).
2. In your `config.yaml`, set `character_pack: "<your_pack_name>"` on the `weather-forecast` widget:

```yaml
- id: home-weather
  type: weather-forecast
  config:
    location_name: "Seattle, WA"
    latitude: 47.6062
    longitude: -122.3321
    units: imperial
    mode: school-day
    character_pack: "my_custom_pack"
```

## Required Image Files

Provide 1:1 square `.jpg` illustrations for each of the 7 weather/temperature scene slots (`_v1.jpg` through `_v5.jpg` per scene, 35 images total):

- `morning_rain_v1.jpg` .. `morning_rain_v5.jpg` — Morning rain, drizzle, showers, or thunderstorms
- `rainy_boots_v1.jpg` .. `rainy_boots_v5.jpg` — Afternoon/evening rain, drizzle, showers, or thunderstorms
- `cold_puffer_v1.jpg` .. `cold_puffer_v5.jpg` — Cold weather (`< 50°F`)
- `morning_mild_v1.jpg` .. `morning_mild_v5.jpg` — Mild daytime weather (`50°F – 64°F`)
- `cool_evening_v1.jpg` .. `cool_evening_v5.jpg` — Cool evening/night (`50°F – 64°F`, `5:00 PM – 5:00 AM`)
- `warm_pants_v1.jpg` .. `warm_pants_v5.jpg` — Warm weather (`65°F – 77°F`)
- `hot_dress_v1.jpg` .. `hot_dress_v5.jpg` — Hot weather (`>= 78°F`)

All subdirectories inside `web/ui/public/assets/weather-packs/` are gitignored by default so personal family artwork stays private to your local deployment.
