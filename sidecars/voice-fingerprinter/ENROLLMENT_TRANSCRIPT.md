# Voice Fingerprint Enrollment Transcript

This document provides the recommended, phonetically balanced enrollment passage for registering speaker voice fingerprints with Mirrormere's `voice-fingerprinter` sidecar using [`enroll.py`](./enroll.py).

## Why This Transcript?

For ECAPA-TDNN speaker verification (`speechbrain/spkrec-ecapa-voxceleb`), `enroll.py` segments recorded audio into ~4-second chunks, filters out near-silent segments (RMS < 10% peak), embeds each chunk, and averages them into an L2-normalized centroid vector.

This combined script provides approximately **45–50 seconds of clean speech (~110 words)**, producing roughly **11–12 surviving 4-second chunks**:
1. **Phonetic Calibration (Rainbow Passage)**: The acoustic science standard for speech research. It is phonetically balanced across English vowels, diphthongs, formants, and fricatives to establish a comprehensive acoustic baseline.
2. **Assistant Flow & Natural Prosody**: Realistic smart display queries and home automation commands that match your authentic speaking cadence, inflection, and room acoustics when talking to Mirrormere.

---

## Recording Guidelines

1. **Environment**: Record in a quiet room with minimal background noise, HVAC hum, or echo.
2. **Microphone Distance**: Stand or sit at your typical distance from the kiosk microphone.
3. **Vocal Delivery**: Speak in your normal conversational tone and natural volume. Avoid robotic pronunciation, whispering, or shouting.
4. **Continuous Reading**: Read smoothly through both paragraphs without trailing off or pausing excessively.

---

## Combined Enrollment Transcript

> When the sunlight strikes raindrops in the air, they act as a prism and form a rainbow. The rainbow is a division of white light into many beautiful colors. These take the shape of a long round arch, with its path high above, and its two ends apparently beyond the horizon.
>
> Hey Aerial, good morning. What’s on the family calendar for today, and how is the weather looking in Oakland? Add dark roast coffee and oat milk to the grocery list, check if the hallway lights are still on, and pull up the commute traffic. Set a timer for the oven in twenty minutes, and play some chill music on the kitchen speakers.

---

## Quick Enrollment Usage

### 1. Record WAV Audio
Record a single-channel (mono) 16kHz WAV file from your kiosk or desk microphone:
```bash
arecord -D hw:CARD=Array,DEV=0 -f S16_LE -r 16000 -c 1 -d 50 sample.wav
```

### 2. Enroll Speaker Fingerprint
Feed the recording to `enroll.py` targeting your Mirrormere `speakers.json`:

```bash
# Running inside the sidecar container
docker compose exec voice-fingerprinter python enroll.py <speaker_id> --wav /path/to/sample.wav --output /data/speakers.json

# Running standalone against the sidecar HTTP endpoint
python sidecars/voice-fingerprinter/enroll.py <speaker_id> --wav sample.wav --embed-url http://localhost:9096/embed --output speakers.json
```
