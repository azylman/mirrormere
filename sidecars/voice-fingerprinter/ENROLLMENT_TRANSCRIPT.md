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
Stop the voice service on the voice node temporarily so it releases the ALSA capture hardware, then record a single-channel (mono) 16kHz WAV file using your node's configured `audio_device` (from `voice.yaml`):

```bash
# On the voice node / ear device:
# Stop the voice client so it releases the audio device:
sudo systemctl stop mirrormere-voice

# Record 50s calibration audio:
arecord -D <audio_device> -f S16_LE -r 16000 -c 1 -d 50 /tmp/enroll-<speaker_id>.wav

# Restart the voice client:
sudo systemctl start mirrormere-voice
```

> [!NOTE]
> **Single-Host vs. Split Deployment**:
> - **Single-host (Hub and Ear on same machine)**: Move the file directly: `mv /tmp/enroll-<speaker_id>.wav deploy/data/enroll-<speaker_id>.wav`.
> - **Split deployment (Ear on satellite Pi, Hub on server)**: Copy the recorded WAV from the voice node to the Hub host's data directory:
>   ```bash
>   scp /tmp/enroll-<speaker_id>.wav <hub-host>:/path/to/mirrormere/deploy/data/enroll-<speaker_id>.wav
>   rm /tmp/enroll-<speaker_id>.wav
>   ```

### 2. Enroll Speaker Fingerprint
On the Hub host, feed the recording to `enroll.py` targeting your Mirrormere `/data/speakers.json`, then delete the temporary recording:

```bash
# Running inside the sidecar container (with ./data mounted to /data):
docker compose exec voice-fingerprinter python enroll.py <speaker_id> --wav /data/enroll-<speaker_id>.wav --output /data/speakers.json

# Audio is never stored long-term (SPEC-011 §Speaker Identification); clean up the WAV on both hosts:
rm deploy/data/enroll-<speaker_id>.wav

# Or running standalone against the sidecar HTTP endpoint:
python sidecars/voice-fingerprinter/enroll.py <speaker_id> --wav deploy/data/enroll-<speaker_id>.wav --embed-url http://localhost:9096/embed --output deploy/data/speakers.json
rm deploy/data/enroll-<speaker_id>.wav
```
