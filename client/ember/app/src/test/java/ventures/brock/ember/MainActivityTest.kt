package ventures.brock.ember

import org.junit.Assert.assertEquals
import org.junit.Assert.assertFalse
import org.junit.Assert.assertNull
import org.junit.Assert.assertTrue
import org.junit.Test

/**
 * Direct JUnit 4 unit tests for MainActivity and MotionLumaMonitor pure companion logic.
 *
 * Exercises:
 * 1. Google Photos URL allowlist and DOM URL extraction.
 * 2. Time string parsing and night quiet-hours schedule calculations.
 * 3. Night schedule state machine decisions with default IDLE_TIMEOUT_MS.
 * 4. Ambient luminance and idle sleep timeout rules.
 * 5. Start URL resolution precedence and cache persistence decisions.
 * 6. Origin extraction, allowlist parsing, bridge guards, and OTA URL filtering.
 * 7. MotionLumaMonitor frame luminance and delta motion detection math.
 */
class MainActivityTest {

    @Test
    fun testIsGooglePhotosAlbumUrlAllowed() {
        assertTrue(MainActivity.isGooglePhotosAlbumUrlAllowed("https://photos.google.com/share/AF1QipExample"))
        assertTrue(MainActivity.isGooglePhotosAlbumUrlAllowed("  https://photos.app.goo.gl/AbCdEf123456  "))
        assertFalse(MainActivity.isGooglePhotosAlbumUrlAllowed("https://evil.example.com/photos.google.com"))
        assertFalse(MainActivity.isGooglePhotosAlbumUrlAllowed("javascript:alert(1)"))
        assertFalse(MainActivity.isGooglePhotosAlbumUrlAllowed(""))
        assertFalse(MainActivity.isGooglePhotosAlbumUrlAllowed("   "))
        assertFalse(MainActivity.isGooglePhotosAlbumUrlAllowed(null))
    }

    @Test
    fun testExtractGooglePhotosUrls() {
        val sampleDom = """
            "https:\/\/lh3.googleusercontent.com\/pw\/AP1GczMockPhotoTokenAlpha1234567890\u003dw1920-h1080"
            "https://lh3.googleusercontent.com/pw/AP1GczMockPhotoTokenBeta0987654321=s0"
            "https://lh3.googleusercontent.com/pw/AP1GczMockPhotoTokenAlpha1234567890=w800"
            "https://lh3.googleusercontent.com/pw/short"
        """.trimIndent()

        val extracted = MainActivity.extractGooglePhotosUrls(sampleDom)
        assertEquals(2, extracted.size)
        assertEquals("https://lh3.googleusercontent.com/pw/AP1GczMockPhotoTokenAlpha1234567890", extracted[0])
        assertEquals("https://lh3.googleusercontent.com/pw/AP1GczMockPhotoTokenBeta0987654321", extracted[1])

        assertTrue(MainActivity.extractGooglePhotosUrls("").isEmpty())
        assertTrue(MainActivity.extractGooglePhotosUrls("   ").isEmpty())
        assertTrue(MainActivity.extractGooglePhotosUrls(null).isEmpty())
    }

    @Test
    fun testParseTimeMinutes() {
        assertEquals(1350, MainActivity.parseTimeMinutes("22:30", 0))
        assertEquals(390, MainActivity.parseTimeMinutes("06:30", 0))
        assertEquals(365, MainActivity.parseTimeMinutes("6:05", 0))
        assertEquals(0, MainActivity.parseTimeMinutes("00:00", 999))
        assertEquals(1439, MainActivity.parseTimeMinutes("23:59", 0))

        // Fallbacks on invalid formatting
        assertEquals(123, MainActivity.parseTimeMinutes("24:00", 123))
        assertEquals(123, MainActivity.parseTimeMinutes("-1:00", 123))
        assertEquals(123, MainActivity.parseTimeMinutes("12:60", 123))
        assertEquals(123, MainActivity.parseTimeMinutes("invalid", 123))
        assertEquals(123, MainActivity.parseTimeMinutes("", 123))
        assertEquals(123, MainActivity.parseTimeMinutes("   ", 123))
        assertEquals(123, MainActivity.parseTimeMinutes("12", 123))
    }

    @Test
    fun testIsNightQuietHours() {
        // Default schedule: 00:00 to 06:30
        assertTrue(MainActivity.isNightQuietHours(0, 0))
        assertTrue(MainActivity.isNightQuietHours(3, 15))
        assertTrue(MainActivity.isNightQuietHours(6, 29))
        assertFalse(MainActivity.isNightQuietHours(6, 30))
        assertFalse(MainActivity.isNightQuietHours(12, 0))
        assertFalse(MainActivity.isNightQuietHours(23, 59))

        // Custom overnight schedule: 22:30 to 06:30
        val startMin = 22 * 60 + 30
        val endMin = 6 * 60 + 30
        assertFalse(MainActivity.isNightQuietHours(22, 29, startMin, endMin))
        assertTrue(MainActivity.isNightQuietHours(22, 30, startMin, endMin))
        assertTrue(MainActivity.isNightQuietHours(23, 59, startMin, endMin))
        assertTrue(MainActivity.isNightQuietHours(0, 0, startMin, endMin))
        assertTrue(MainActivity.isNightQuietHours(3, 15, startMin, endMin))
        assertTrue(MainActivity.isNightQuietHours(6, 29, startMin, endMin))
        assertFalse(MainActivity.isNightQuietHours(6, 30, startMin, endMin))
        assertFalse(MainActivity.isNightQuietHours(12, 0, startMin, endMin))

        // Daytime schedule: 09:00 to 17:00
        val dayStart = 9 * 60
        val dayEnd = 17 * 60
        assertFalse(MainActivity.isNightQuietHours(8, 59, dayStart, dayEnd))
        assertTrue(MainActivity.isNightQuietHours(9, 0, dayStart, dayEnd))
        assertTrue(MainActivity.isNightQuietHours(16, 59, dayStart, dayEnd))
        assertFalse(MainActivity.isNightQuietHours(17, 0, dayStart, dayEnd))

        // Empty window: start == end
        assertFalse(MainActivity.isNightQuietHours(10, 0, 10 * 60, 10 * 60))

        // isWithinNightQuietHours alias parity
        assertTrue(MainActivity.isWithinNightQuietHours(0, 0))
        assertFalse(MainActivity.isWithinNightQuietHours(12, 0))
    }

    @Test
    fun testDecideNightSchedule() {
        val t0 = 1_000_000L

        // Daytime, not in night window -> NONE
        val d1 = MainActivity.decideNightSchedule(
            hour = 12,
            minute = 0,
            wasInNightWindow = false,
            isAsleep = false,
            nowMs = t0,
            lastTouchOrWakeTimeMs = t0
        )
        assertEquals(NightScheduleAction.NONE, d1.action)
        assertFalse(d1.inNightWindow)

        // Entering night window, but idle timeout has not elapsed -> NONE, inNightWindow = true
        val d2 = MainActivity.decideNightSchedule(
            hour = 0,
            minute = 0,
            wasInNightWindow = false,
            isAsleep = false,
            nowMs = t0 + 10 * 60 * 1000L,
            lastTouchOrWakeTimeMs = t0
        )
        assertEquals(NightScheduleAction.NONE, d2.action)
        assertTrue(d2.inNightWindow)

        // In night window, idle timeout elapsed (15 min) -> SLEEP
        val d3 = MainActivity.decideNightSchedule(
            hour = 0,
            minute = 5,
            wasInNightWindow = true,
            isAsleep = false,
            nowMs = t0 + 15 * 60 * 1000L,
            lastTouchOrWakeTimeMs = t0
        )
        assertEquals(NightScheduleAction.SLEEP, d3.action)
        assertTrue(d3.inNightWindow)

        // In night window, already asleep -> NONE
        val d4 = MainActivity.decideNightSchedule(
            hour = 1,
            minute = 0,
            wasInNightWindow = true,
            isAsleep = true,
            nowMs = t0 + 60 * 60 * 1000L,
            lastTouchOrWakeTimeMs = t0
        )
        assertEquals(NightScheduleAction.NONE, d4.action)
        assertTrue(d4.inNightWindow)

        // Exiting night quiet hours (morning transition at 06:30) -> WAKE
        val d5 = MainActivity.decideNightSchedule(
            hour = 6,
            minute = 30,
            wasInNightWindow = true,
            isAsleep = true,
            nowMs = t0 + 390 * 60 * 1000L,
            lastTouchOrWakeTimeMs = t0
        )
        assertEquals(NightScheduleAction.WAKE, d5.action)
        assertFalse(d5.inNightWindow)

        // Verify default idleTimeoutMs parameter matches IDLE_TIMEOUT_MS (900_000 ms)
        val dDefaultSleep = MainActivity.decideNightSchedule(
            hour = 2,
            minute = 0,
            wasInNightWindow = true,
            isAsleep = false,
            nowMs = MainActivity.IDLE_TIMEOUT_MS,
            lastTouchOrWakeTimeMs = 0L
        )
        assertEquals(NightScheduleAction.SLEEP, dDefaultSleep.action)

        val dDefaultNoSleep = MainActivity.decideNightSchedule(
            hour = 2,
            minute = 0,
            wasInNightWindow = true,
            isAsleep = false,
            nowMs = MainActivity.IDLE_TIMEOUT_MS - 1L,
            lastTouchOrWakeTimeMs = 0L
        )
        assertEquals(NightScheduleAction.NONE, dDefaultNoSleep.action)
    }

    @Test
    fun testShouldSleepOnAmbientLuma() {
        val t0 = 1000L

        // Already asleep -> false
        assertFalse(
            MainActivity.shouldSleepOnAmbientLuma(
                avgLuma = 4.0,
                isAsleep = true,
                nowMs = t0 + 40_000L,
                lastWakeTimeMs = t0
            )
        )

        // Normal daylight/ambient light (luma >= threshold) -> false
        assertFalse(
            MainActivity.shouldSleepOnAmbientLuma(
                avgLuma = 80.0,
                isAsleep = false,
                nowMs = t0 + 40_000L,
                lastWakeTimeMs = t0
            )
        )

        // Luma exactly equal to threshold -> false (only < threshold sleeps)
        assertFalse(
            MainActivity.shouldSleepOnAmbientLuma(
                avgLuma = MainActivity.DARKNESS_LUMA_THRESHOLD,
                isAsleep = false,
                nowMs = t0 + 40_000L,
                lastWakeTimeMs = t0
            )
        )

        // Darkness within grace period (elapsed <= WAKE_GRACE_PERIOD_MS) -> false
        assertFalse(
            MainActivity.shouldSleepOnAmbientLuma(
                avgLuma = 5.0,
                isAsleep = false,
                nowMs = t0 + 20_000L,
                lastWakeTimeMs = t0
            )
        )

        // Darkness after grace period (elapsed > WAKE_GRACE_PERIOD_MS) -> true
        assertTrue(
            MainActivity.shouldSleepOnAmbientLuma(
                avgLuma = 5.0,
                isAsleep = false,
                nowMs = t0 + 35_000L,
                lastWakeTimeMs = t0
            )
        )

        // Verify default threshold and grace period parameters
        assertTrue(
            MainActivity.shouldSleepOnAmbientLuma(
                avgLuma = 7.9,
                isAsleep = false,
                nowMs = MainActivity.WAKE_GRACE_PERIOD_MS + 1L,
                lastWakeTimeMs = 0L
            )
        )
        assertFalse(
            MainActivity.shouldSleepOnAmbientLuma(
                avgLuma = 7.9,
                isAsleep = false,
                nowMs = MainActivity.WAKE_GRACE_PERIOD_MS,
                lastWakeTimeMs = 0L
            )
        )
    }

    @Test
    fun testShouldSleepOnIdleTimeout() {
        // Already asleep -> false
        assertFalse(
            MainActivity.shouldSleepOnIdleTimeout(
                isAsleep = true,
                motionMonitorRunning = true,
                hour = 12,
                minute = 0
            )
        )

        // Motion monitor running and idle timeout elapsed -> true
        assertTrue(
            MainActivity.shouldSleepOnIdleTimeout(
                isAsleep = false,
                motionMonitorRunning = true,
                hour = 12,
                minute = 0,
                nowMs = MainActivity.IDLE_TIMEOUT_MS,
                lastTouchOrWakeTimeMs = 0L,
                idleTimeoutMs = MainActivity.IDLE_TIMEOUT_MS
            )
        )

        // Motion monitor running but idle timeout not elapsed -> false
        assertFalse(
            MainActivity.shouldSleepOnIdleTimeout(
                isAsleep = false,
                motionMonitorRunning = true,
                hour = 12,
                minute = 0,
                nowMs = MainActivity.IDLE_TIMEOUT_MS - 1L,
                lastTouchOrWakeTimeMs = 0L,
                idleTimeoutMs = MainActivity.IDLE_TIMEOUT_MS
            )
        )

        // Motion monitor stopped during daytime (not in night window) -> false
        assertFalse(
            MainActivity.shouldSleepOnIdleTimeout(
                isAsleep = false,
                motionMonitorRunning = false,
                hour = 12,
                minute = 0,
                nowMs = MainActivity.IDLE_TIMEOUT_MS,
                lastTouchOrWakeTimeMs = 0L,
                idleTimeoutMs = MainActivity.IDLE_TIMEOUT_MS
            )
        )

        // Motion monitor stopped during night quiet hours, idle timeout elapsed -> true
        assertTrue(
            MainActivity.shouldSleepOnIdleTimeout(
                isAsleep = false,
                motionMonitorRunning = false,
                hour = 2,
                minute = 0,
                nowMs = MainActivity.IDLE_TIMEOUT_MS,
                lastTouchOrWakeTimeMs = 0L,
                idleTimeoutMs = MainActivity.IDLE_TIMEOUT_MS
            )
        )

        // Verify default parameters bind to IDLE_TIMEOUT_MS and elapsed >= idleTimeoutMs
        assertTrue(
            MainActivity.shouldSleepOnIdleTimeout(
                isAsleep = false,
                motionMonitorRunning = true,
                hour = 12,
                minute = 0
            )
        )
    }

    @Test
    fun testResolveStartUrlValue() {
        val defaultUrl = MainActivity.DEFAULT_START_URL

        // Intent extra overrides cached prefs and instructs persistence
        val (u1, p1) = MainActivity.resolveStartUrlValue("https://hearth.local/custom", "https://hearth.local/cached")
        assertEquals("https://hearth.local/custom", u1)
        assertEquals("https://hearth.local/custom", p1)

        // No intent extra falls back to cached prefs (no persistence needed)
        val (u2, p2) = MainActivity.resolveStartUrlValue(null, "https://hearth.local/cached")
        assertEquals("https://hearth.local/cached", u2)
        assertNull(p2)

        // Blank intent extra falls back to cached prefs
        val (u3, p3) = MainActivity.resolveStartUrlValue("   ", "https://hearth.local/cached")
        assertEquals("https://hearth.local/cached", u3)
        assertNull(p3)

        // Neither intent extra nor cached prefs falls back to default URL
        val (u4, p4) = MainActivity.resolveStartUrlValue(null, null)
        assertEquals(defaultUrl, u4)
        assertNull(p4)

        // Blank cached prefs falls back to default URL
        val (u5, p5) = MainActivity.resolveStartUrlValue(null, "   ")
        assertEquals(defaultUrl, u5)
        assertNull(p5)

        // Custom default URL parameter
        val (u6, p6) = MainActivity.resolveStartUrlValue(null, null, "https://custom.display")
        assertEquals("https://custom.display", u6)
        assertNull(p6)
    }

    @Test
    fun testExtractOrigin() {
        assertEquals("https://hearth.local", MainActivity.extractOrigin("https://hearth.local/display?hide_cursor=true"))
        assertEquals("http://kiosk.local:8080", MainActivity.extractOrigin("http://kiosk.local:8080/display#top"))
        assertEquals("http://203.0.113.10:8123", MainActivity.extractOrigin("http://203.0.113.10:8123/lovelace/home"))
        assertEquals("https://203.0.113.10", MainActivity.extractOrigin("https://203.0.113.10/lovelace"))

        // Default ports omitted per RFC 6454
        assertEquals("http://example.com", MainActivity.extractOrigin("http://example.com:80/path"))
        assertEquals("https://example.com", MainActivity.extractOrigin("https://example.com:443/path"))

        // Non-default ports preserved
        assertEquals("https://example.com:8443", MainActivity.extractOrigin("https://example.com:8443/secure"))
        assertEquals("http://example.com:443", MainActivity.extractOrigin("http://example.com:443/odd"))
        assertEquals("https://example.com:80", MainActivity.extractOrigin("https://example.com:80/odd"))

        // Case normalization
        assertEquals("https://hearth.local:8080", MainActivity.extractOrigin("HTTPS://HEARTH.LOCAL:8080/display"))

        // Invalid, empty, or non-http(s) schemes return null
        assertNull(MainActivity.extractOrigin(null))
        assertNull(MainActivity.extractOrigin(""))
        assertNull(MainActivity.extractOrigin("   "))
        assertNull(MainActivity.extractOrigin("not-a-valid-url"))
        assertNull(MainActivity.extractOrigin("ftp://files.example.com"))
        assertNull(MainActivity.extractOrigin("javascript:alert(1)"))
        assertNull(MainActivity.extractOrigin("file:///sdcard/app.apk"))
    }

    @Test
    fun testParseAllowedOrigins() {
        // Auto-trust start_url origin
        val o1 = MainActivity.parseAllowedOrigins("https://hearth.local/display?hide_cursor=true")
        assertEquals(setOf("https://hearth.local"), o1)

        // Extra origins via CSV
        val o2 = MainActivity.parseAllowedOrigins(
            "https://hearth.local/display",
            "https://example.org, http://203.0.113.50:8080"
        )
        assertEquals(
            setOf("https://hearth.local", "https://example.org", "http://203.0.113.50:8080"),
            o2
        )

        // Handling whitespace, trailing commas, empty tokens
        val o3 = MainActivity.parseAllowedOrigins(
            "https://hearth.local",
            "  ,  https://extra.local  ,  , "
        )
        assertEquals(setOf("https://hearth.local", "https://extra.local"), o3)

        // Scheme-less extra origin tokens expand to both https and http
        val o4 = MainActivity.parseAllowedOrigins("https://hearth.local", "frame.example.com:8080")
        assertEquals(
            setOf("https://hearth.local", "https://frame.example.com:8080", "http://frame.example.com:8080"),
            o4
        )

        // Empty inputs
        assertEquals(emptySet<String>(), MainActivity.parseAllowedOrigins(null, null))
        assertEquals(emptySet<String>(), MainActivity.parseAllowedOrigins("", ""))
    }

    @Test
    fun testIsBridgeOriginAllowed() {
        val start = "https://hearth.local/display?hide_cursor=true"

        assertTrue(MainActivity.isBridgeOriginAllowed("https://hearth.local/display", start))
        assertTrue(MainActivity.isBridgeOriginAllowed("https://hearth.local/settings/audio?debug=true", start))

        assertFalse(MainActivity.isBridgeOriginAllowed("https://malicious.evil.com/phish", start))
        assertFalse(MainActivity.isBridgeOriginAllowed("http://hearth.local/display", start)) // Protocol mismatch
        assertFalse(MainActivity.isBridgeOriginAllowed("https://hearth.local:8443/display", start)) // Port mismatch
        assertFalse(MainActivity.isBridgeOriginAllowed(null, start))
        assertFalse(MainActivity.isBridgeOriginAllowed("", start))
        assertFalse(MainActivity.isBridgeOriginAllowed("invalid-url", start))

        // Multi-origin setup via bridge_origins
        val extras = "https://frame.example.com:8080, http://203.0.113.30:3000"
        assertTrue(MainActivity.isBridgeOriginAllowed("https://hearth.local/display", start, extras))
        assertTrue(MainActivity.isBridgeOriginAllowed("https://frame.example.com:8080/home", start, extras))
        assertTrue(MainActivity.isBridgeOriginAllowed("http://203.0.113.30:3000/dash", start, extras))
        assertFalse(MainActivity.isBridgeOriginAllowed("http://203.0.113.30:8080/other", start, extras))
    }

    @Test
    fun testIsOtaUrlAllowed() {
        val start = "https://hearth.local/display"

        // Authorized OTA download from start_url origin
        assertTrue(MainActivity.isOtaUrlAllowed("https://hearth.local/ota/ember-v2.apk", start))
        assertTrue(
            MainActivity.isOtaUrlAllowed(
                "https://hearth.local/downloads/ember-release.apk?version=2.1&token=xyz",
                start
            )
        )

        // OTA disabled flag blocks even authorized origins
        assertFalse(
            MainActivity.isOtaUrlAllowed(
                "https://hearth.local/ota/ember-v2.apk",
                start,
                otaEnabled = false
            )
        )

        // Untrusted origins blocked
        assertFalse(MainActivity.isOtaUrlAllowed("https://evil.com/ota/ember-v2.apk", start))
        assertFalse(MainActivity.isOtaUrlAllowed("http://hearth.local/ota/ember-v2.apk", start))

        // Non-APK extensions blocked
        assertFalse(MainActivity.isOtaUrlAllowed("https://hearth.local/ota/script.sh", start))
        assertFalse(MainActivity.isOtaUrlAllowed("https://hearth.local/ota/ember.apk.tar.gz", start))
        assertFalse(MainActivity.isOtaUrlAllowed("https://hearth.local/ota/ember.exe", start))

        // Configured extra origin for updates allowed
        val extras = "https://updates.example.org:8443"
        assertTrue(
            MainActivity.isOtaUrlAllowed(
                "https://updates.example.org:8443/packages/ember-latest.apk",
                start,
                extraOriginsCsv = extras
            )
        )

        // Malformed or blank URLs blocked
        assertFalse(MainActivity.isOtaUrlAllowed("", start))
        assertFalse(MainActivity.isOtaUrlAllowed("   ", start))
        assertFalse(MainActivity.isOtaUrlAllowed("not-a-url", start))
    }

    @Test
    fun testComputeFrameLumaAndDelta() {
        val totalPixels = 160 * 120

        // Test 1: Uniform dark frame (luma = 10) with no previous frame
        val darkFrame = ByteArray(totalPixels) { 10.toByte() }
        val (lumaDarkInit, deltaInit, motionInit) = MotionLumaMonitor.computeFrameLumaAndDelta(
            currentFrame = darkFrame,
            prevFrame = null,
            motionThreshold = 12.0
        )
        assertEquals(10.0, lumaDarkInit, 0.001)
        assertEquals(0.0, deltaInit, 0.001)
        assertFalse(motionInit)

        // Test 2: Identical consecutive frames (delta = 0.0)
        val (lumaDark, deltaSame, motionSame) = MotionLumaMonitor.computeFrameLumaAndDelta(
            currentFrame = darkFrame,
            prevFrame = darkFrame,
            motionThreshold = 12.0
        )
        assertEquals(10.0, lumaDark, 0.001)
        assertEquals(0.0, deltaSame, 0.001)
        assertFalse(motionSame)

        // Test 3: Minor sensor noise (delta 3.0 < 12.0 threshold)
        val noiseFrame = ByteArray(totalPixels) { 13.toByte() }
        val (lumaNoise, deltaNoise, motionNoise) = MotionLumaMonitor.computeFrameLumaAndDelta(
            currentFrame = noiseFrame,
            prevFrame = darkFrame,
            motionThreshold = 12.0
        )
        assertEquals(13.0, lumaNoise, 0.001)
        assertEquals(3.0, deltaNoise, 0.001)
        assertFalse(motionNoise)

        // Test 4: Significant motion (delta 15.0 >= 12.0 threshold)
        val motionFrame = ByteArray(totalPixels) { 25.toByte() }
        val (lumaMotion, deltaMotion, motionDetected) = MotionLumaMonitor.computeFrameLumaAndDelta(
            currentFrame = motionFrame,
            prevFrame = darkFrame,
            motionThreshold = 12.0
        )
        assertEquals(25.0, lumaMotion, 0.001)
        assertEquals(15.0, deltaMotion, 0.001)
        assertTrue(motionDetected)

        // Test 5: Inverted scene motion where whole-frame average luma is unchanged
        // Frame A: half 0, half 100 -> avg luma = 50.0
        // Frame B: half 100, half 0 -> avg luma = 50.0
        // Whole-frame delta is 0.0, but pixel-by-pixel mean delta is 100.0 >= 12.0!
        val splitA = ByteArray(totalPixels) { if (it < totalPixels / 2) 0.toByte() else 100.toByte() }
        val splitB = ByteArray(totalPixels) { if (it < totalPixels / 2) 100.toByte() else 0.toByte() }
        val (lumaSplitB, deltaSplit, motionSplit) = MotionLumaMonitor.computeFrameLumaAndDelta(
            currentFrame = splitB,
            prevFrame = splitA,
            motionThreshold = 12.0
        )
        assertEquals(50.0, lumaSplitB, 0.001)
        assertEquals(100.0, deltaSplit, 0.001)
        assertTrue(motionSplit)

        // Test 6: Bright scene (luma = 220)
        val brightFrame = ByteArray(totalPixels) { 220.toByte() }
        val (lumaBright, _, _) = MotionLumaMonitor.computeFrameLumaAndDelta(
            currentFrame = brightFrame,
            prevFrame = brightFrame
        )
        assertEquals(220.0, lumaBright, 0.001)

        // Test 7: Empty frame
        val (emptyLuma, emptyDelta, emptyMotion) = MotionLumaMonitor.computeFrameLumaAndDelta(
            currentFrame = ByteArray(0),
            prevFrame = null
        )
        assertEquals(0.0, emptyLuma, 0.001)
        assertEquals(0.0, emptyDelta, 0.001)
        assertFalse(emptyMotion)

        // Test 8: Frame size mismatch ignored for motion
        val (mismatchLuma, mismatchDelta, mismatchMotion) = MotionLumaMonitor.computeFrameLumaAndDelta(
            currentFrame = darkFrame,
            prevFrame = ByteArray(10) { 100.toByte() },
            motionThreshold = 12.0
        )
        assertEquals(10.0, mismatchLuma, 0.001)
        assertEquals(0.0, mismatchDelta, 0.001)
        assertFalse(mismatchMotion)

        // Test 9: Default motionThreshold parameter verification
        val (_, deltaDefault, motionDefault) = MotionLumaMonitor.computeFrameLumaAndDelta(
            currentFrame = ByteArray(totalPixels) { (10 + MotionLumaMonitor.DEFAULT_MOTION_THRESHOLD.toInt()).toByte() },
            prevFrame = darkFrame
        )
        assertEquals(MotionLumaMonitor.DEFAULT_MOTION_THRESHOLD, deltaDefault, 0.001)
        assertTrue(motionDefault)
    }
}
