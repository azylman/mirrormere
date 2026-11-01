package ventures.brock.ember

import android.Manifest
import android.annotation.SuppressLint
import android.app.Activity
import android.app.AlertDialog
import android.app.PendingIntent
import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.content.pm.PackageInstaller
import android.content.pm.PackageManager
import android.graphics.Bitmap
import android.graphics.Color
import android.os.Build
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.util.Log
import android.view.MotionEvent
import android.view.View
import android.view.WindowInsets
import android.view.WindowInsetsController
import android.view.WindowManager
import android.webkit.CookieManager
import android.webkit.JavascriptInterface
import android.webkit.PermissionRequest
import android.webkit.WebChromeClient
import android.webkit.WebResourceError
import android.webkit.WebResourceRequest
import android.webkit.WebResourceResponse
import android.webkit.WebSettings
import android.webkit.WebView
import android.webkit.WebViewClient
import android.widget.FrameLayout
import java.io.File
import java.net.HttpURLConnection
import java.net.URI
import java.net.URL
import org.json.JSONArray
import org.json.JSONObject
import java.util.Calendar

enum class NightScheduleAction {
    NONE,
    WAKE,
    SLEEP
}

data class NightScheduleDecision(
    val action: NightScheduleAction,
    val inNightWindow: Boolean
)

/**
 * Native EMBER Kiosk MainActivity.
 *
 * Implements:
 * 1. Fullscreen immersive kiosk WebView with auto-reconnect on network/render errors.
 * 2. Start URL intent extra handling with persistent SharedPreferences caching and neutral fallback.
 * 3. WebChromeClient auto-granting quad-mic and camera permissions for voice/video bridging.
 * 4. Ambient darkness dimming and Camera2 motion wake via MotionLumaMonitor.
 * 5. Screen brightness management (0.01f asleep with black overlay, -1.0f awake) and touch wake.
 * 6. Rockchip Android 16 Wi-Fi safety: zero Wi-Fi subsystem calls or state locks.
 * 7. Built-in night quiet-hours schedule (default 00:00 to 06:30) with 15-minute touch wake and morning auto-wake.
 */
class MainActivity : Activity() {

    companion object {
        private const val TAG = "MainActivity"
        const val PREFS_NAME = "ember_prefs"
        const val KEY_START_URL = "start_url"
        const val EXTRA_START_URL = "start_url"
        const val DEFAULT_START_URL = "https://hearth.local/display?hide_cursor=true"
        const val IDLE_TIMEOUT_MS = 15 * 60 * 1000L // 15 minutes
        const val DARKNESS_LUMA_THRESHOLD = 8.0
        const val SLEEP_BRIGHTNESS = 0.01f
        const val AWAKE_BRIGHTNESS = -1.0f
        const val RETRY_DELAY_MS = 5000L
        const val WAKE_GRACE_PERIOD_MS = 30_000L
        const val PERMISSION_REQUEST_CODE = 2001

        const val DEFAULT_NIGHT_START = "00:00"
        const val DEFAULT_NIGHT_END = "06:30"
        const val KEY_NIGHT_START = "night_start"
        const val KEY_NIGHT_END = "night_end"
        const val EXTRA_NIGHT_START = "night_start"
        const val EXTRA_NIGHT_END = "night_end"
        const val NIGHT_CHECK_INTERVAL_MS = 60_000L
        const val PHOTOS_SYNC_TIMEOUT_MS = 30_000L

        const val KEY_BRIDGE_ORIGINS = "bridge_origins"
        const val EXTRA_BRIDGE_ORIGINS = "bridge_origins"
        const val KEY_OTA_ENABLED = "ota_enabled"
        const val EXTRA_OTA_ENABLED = "ota_enabled"
        const val KEY_ALLOW_MIXED_CONTENT = "allow_mixed_content"
        const val EXTRA_ALLOW_MIXED_CONTENT = "allow_mixed_content"
        const val DEFAULT_OTA_ENABLED = true
        const val DEFAULT_ALLOW_MIXED_CONTENT = false

        fun parseTimeMinutes(hhmm: String, fallbackMinutes: Int): Int {
            return try {
                val parts = hhmm.trim().split(":")
                if (parts.size != 2) return fallbackMinutes
                val h = parts[0].toInt()
                val m = parts[1].toInt()
                if (h in 0..23 && m in 0..59) {
                    h * 60 + m
                } else {
                    fallbackMinutes
                }
            } catch (e: Exception) {
                fallbackMinutes
            }
        }

        fun isNightQuietHours(
            hour: Int,
            minute: Int,
            startMinutes: Int = 0,
            endMinutes: Int = 6 * 60 + 30
        ): Boolean {
            val current = hour * 60 + minute
            return if (startMinutes < endMinutes) {
                current in startMinutes until endMinutes
            } else if (startMinutes > endMinutes) {
                current >= startMinutes || current < endMinutes
            } else {
                false
            }
        }

        fun isWithinNightQuietHours(
            hour: Int,
            minute: Int,
            startMinutes: Int = 0,
            endMinutes: Int = 6 * 60 + 30
        ): Boolean = isNightQuietHours(hour, minute, startMinutes, endMinutes)

        fun decideNightSchedule(
            hour: Int,
            minute: Int,
            startMinutes: Int = 0,
            endMinutes: Int = 6 * 60 + 30,
            wasInNightWindow: Boolean,
            isAsleep: Boolean,
            nowMs: Long,
            lastTouchOrWakeTimeMs: Long,
            idleTimeoutMs: Long = IDLE_TIMEOUT_MS
        ): NightScheduleDecision {
            val inNight = isNightQuietHours(hour, minute, startMinutes, endMinutes)
            val action = if (wasInNightWindow && !inNight) {
                NightScheduleAction.WAKE
            } else if (inNight && !isAsleep && (nowMs - lastTouchOrWakeTimeMs) >= idleTimeoutMs) {
                NightScheduleAction.SLEEP
            } else {
                NightScheduleAction.NONE
            }
            return NightScheduleDecision(action = action, inNightWindow = inNight)
        }

        fun shouldSleepOnIdleTimeout(
            isAsleep: Boolean,
            motionMonitorRunning: Boolean,
            hour: Int,
            minute: Int,
            startMinutes: Int = 0,
            endMinutes: Int = 6 * 60 + 30,
            nowMs: Long = IDLE_TIMEOUT_MS,
            lastTouchOrWakeTimeMs: Long = 0L,
            idleTimeoutMs: Long = IDLE_TIMEOUT_MS
        ): Boolean {
            if (isAsleep) return false
            val inNight = isNightQuietHours(hour, minute, startMinutes, endMinutes)
            if (!motionMonitorRunning && !inNight) return false
            val elapsed = nowMs - lastTouchOrWakeTimeMs
            return elapsed >= idleTimeoutMs
        }

        fun shouldSleepOnAmbientLuma(
            avgLuma: Double,
            isAsleep: Boolean,
            nowMs: Long,
            lastWakeTimeMs: Long,
            threshold: Double = DARKNESS_LUMA_THRESHOLD,
            gracePeriodMs: Long = WAKE_GRACE_PERIOD_MS
        ): Boolean {
            if (isAsleep) return false
            if (avgLuma >= threshold) return false
            val elapsed = nowMs - lastWakeTimeMs
            return elapsed > gracePeriodMs
        }

        fun resolveStartUrlValue(
            intentUrl: String?,
            savedUrl: String?,
            defaultUrl: String = DEFAULT_START_URL
        ): Pair<String, String?> {
            if (!intentUrl.isNullOrBlank()) {
                val trimmed = intentUrl.trim()
                return Pair(trimmed, trimmed)
            }
            if (!savedUrl.isNullOrBlank()) {
                val trimmed = savedUrl.trim()
                return Pair(trimmed, null)
            }
            return Pair(defaultUrl, null)
        }

        fun extractOrigin(urlString: String?): String? {
            if (urlString.isNullOrBlank()) return null
            return try {
                val trimmed = urlString.trim()
                val uri = URI(trimmed)
                val scheme = uri.scheme?.lowercase() ?: return null
                if (scheme != "http" && scheme != "https") return null
                val host = uri.host?.lowercase() ?: return null
                val port = uri.port
                if (port != -1 && !((scheme == "http" && port == 80) || (scheme == "https" && port == 443))) {
                    "$scheme://$host:$port"
                } else {
                    "$scheme://$host"
                }
            } catch (e: Exception) {
                try {
                    val url = URL(urlString.trim())
                    val protocol = url.protocol?.lowercase() ?: return null
                    if (protocol != "http" && protocol != "https") return null
                    val host = url.host?.lowercase() ?: return null
                    val port = url.port
                    if (port != -1 && !((protocol == "http" && port == 80) || (protocol == "https" && port == 443))) {
                        "$protocol://$host:$port"
                    } else {
                        "$protocol://$host"
                    }
                } catch (e2: Exception) {
                    null
                }
            }
        }

        fun parseAllowedOrigins(startUrl: String?, extraOriginsCsv: String? = null): Set<String> {
            val origins = mutableSetOf<String>()
            extractOrigin(startUrl)?.let { origins.add(it) }
            if (!extraOriginsCsv.isNullOrBlank()) {
                extraOriginsCsv.split(",").forEach { token ->
                    val trimmed = token.trim()
                    if (trimmed.isNotEmpty()) {
                        val origin = extractOrigin(trimmed)
                        if (origin != null) {
                            origins.add(origin)
                        } else if (!trimmed.contains("://")) {
                            extractOrigin("https://$trimmed")?.let { origins.add(it) }
                            extractOrigin("http://$trimmed")?.let { origins.add(it) }
                        }
                    }
                }
            }
            return origins
        }

        fun isBridgeOriginAllowed(
            currentUrl: String?,
            startUrl: String?,
            extraOriginsCsv: String? = null
        ): Boolean {
            if (currentUrl.isNullOrBlank()) return false
            val currentOrigin = extractOrigin(currentUrl) ?: return false
            val allowed = parseAllowedOrigins(startUrl, extraOriginsCsv)
            return allowed.contains(currentOrigin)
        }

        fun isOtaUrlAllowed(
            apkUrl: String,
            startUrl: String?,
            extraOriginsCsv: String? = null,
            otaEnabled: Boolean = true
        ): Boolean {
            if (!otaEnabled) return false
            if (apkUrl.isBlank()) return false
            val otaOrigin = extractOrigin(apkUrl) ?: return false
            val allowed = parseAllowedOrigins(startUrl, extraOriginsCsv)
            if (!allowed.contains(otaOrigin)) return false
            val cleanPath = try {
                URI(apkUrl.trim()).path?.lowercase() ?: ""
            } catch (e: Exception) {
                apkUrl.split("?", "#")[0].lowercase()
            }
            return cleanPath.endsWith(".apk")
        }

        private val GOOGLE_PHOTOS_URL_REGEX =
            Regex("https://lh3\\.googleusercontent\\.com/pw/[A-Za-z0-9_-]{20,}")

        fun isGooglePhotosAlbumUrlAllowed(albumUrl: String?): Boolean {
            if (albumUrl.isNullOrBlank()) return false
            val trimmed = albumUrl.trim()
            return trimmed.startsWith("https://photos.google.com/") ||
                trimmed.startsWith("https://photos.app.goo.gl/")
        }

        fun extractGooglePhotosUrls(html: String?): List<String> {
            if (html.isNullOrBlank()) return emptyList()
            val normalized = html.replace("\\u003d", "=").replace("\\/", "/")
            return GOOGLE_PHOTOS_URL_REGEX.findAll(normalized)
                .map { it.value }
                .distinct()
                .toList()
        }
    }

    lateinit var webView: WebView
        private set
    lateinit var sleepOverlay: View
        private set
    lateinit var motionLumaMonitor: MotionLumaMonitor
        private set

    val mainHandler = Handler(Looper.getMainLooper())

    @Volatile
    var isAsleep: Boolean = false
        private set

    @Volatile
    var currentPageUrl: String? = null
        private set

    var wasInNightWindow: Boolean = false
        private set

    var lastWakeTime: Long = 0L
        private set

    var lastTouchOrWakeTime: Long = 0L
        private set

    private var reloadPending: Boolean = false
    private var activeSyncWebView: WebView? = null
    private var activeAuthDialog: AlertDialog? = null
    private var activeSyncTimeoutRunnable: Runnable? = null
    @Volatile
    private var isPhotosSyncInFlight: Boolean = false

    private val idleRunnable = Runnable {
        val (startMin, endMin) = resolveNightSchedule()
        val cal = Calendar.getInstance()
        val now = System.currentTimeMillis()
        if (shouldSleepOnIdleTimeout(
                isAsleep = isAsleep,
                motionMonitorRunning = motionLumaMonitor.running,
                hour = cal.get(Calendar.HOUR_OF_DAY),
                minute = cal.get(Calendar.MINUTE),
                startMinutes = startMin,
                endMinutes = endMin,
                nowMs = now,
                lastTouchOrWakeTimeMs = lastTouchOrWakeTime,
                idleTimeoutMs = IDLE_TIMEOUT_MS
            )
        ) {
            Log.i(TAG, "Idle timeout reached ($IDLE_TIMEOUT_MS ms) - dimming to sleep")
            sleep()
        }
    }

    private val reloadRunnable = Runnable {
        reloadPending = false
        if (!isDestroyed && !isFinishing) {
            Log.i(TAG, "Auto-reconnecting WebView...")
            webView.reload()
        }
    }

    private val nightScheduleRunnable = object : Runnable {
        override fun run() {
            evaluateNightSchedule()
            mainHandler.postDelayed(this, NIGHT_CHECK_INTERVAL_MS)
        }
    }

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)

        handleNightScheduleIntent(intent)
        handleSecurityConfigIntent(intent)

        motionLumaMonitor = MotionLumaMonitor(
            context = this,
            listener = object : MotionLumaMonitor.Listener {
                override fun onMotionDetected() {
                    wake()
                }

                override fun onAmbientLuma(avgLuma: Double) {
                    handleAmbientLuma(avgLuma)
                }
            }
        )

        setupViews()
        setImmersiveMode()
        configureWebView()

        val initialUrl = resolveStartUrl(intent)
        loadTargetUrl(initialUrl)

        checkAndRequestPermissions()
        resetIdleTimer()
    }

    override fun onNewIntent(intent: Intent?) {
        super.onNewIntent(intent)
        setIntent(intent)
        handleNightScheduleIntent(intent)
        handleSecurityConfigIntent(intent)
        if (::webView.isInitialized) {
            val allowMixed = resolveAllowMixedContent()
            webView.settings.mixedContentMode = if (allowMixed) {
                WebSettings.MIXED_CONTENT_ALWAYS_ALLOW
            } else {
                WebSettings.MIXED_CONTENT_NEVER_ALLOW
            }
        }
        val newUrl = intent?.getStringExtra(EXTRA_START_URL)
        if (!newUrl.isNullOrBlank()) {
            saveStartUrl(newUrl)
            loadTargetUrl(newUrl)
        }
    }

    override fun onResume() {
        super.onResume()
        setImmersiveMode()
        webView.onResume()
        resetIdleTimer()
        startNightScheduleCheck()
        if (checkSelfPermission(Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) {
            startMotionMonitor()
        }
    }

    override fun onPause() {
        super.onPause()
        webView.onPause()
        stopMotionMonitor()
        stopNightScheduleCheck()
    }

    override fun onDestroy() {
        super.onDestroy()
        stopMotionMonitor()
        stopNightScheduleCheck()
        cleanupPhotosSync()
        mainHandler.removeCallbacksAndMessages(null)
        webView.destroy()
    }

    override fun onWindowFocusChanged(hasFocus: Boolean) {
        super.onWindowFocusChanged(hasFocus)
        if (hasFocus) {
            setImmersiveMode()
        }
    }

    @Suppress("DEPRECATION")
    override fun onBackPressed() {
        if (webView.canGoBack()) {
            webView.goBack()
        }
    }

    override fun dispatchTouchEvent(ev: MotionEvent?): Boolean {
        lastTouchOrWakeTime = System.currentTimeMillis()
        resetIdleTimer()
        if (isAsleep) {
            wake()
        }
        return super.dispatchTouchEvent(ev)
    }

    fun sleep() {
        if (isAsleep) return
        isAsleep = true
        Log.i(TAG, "Entering sleep mode (screenBrightness = $SLEEP_BRIGHTNESS)")
        runOnUiThread {
            val lp = window.attributes
            lp.screenBrightness = SLEEP_BRIGHTNESS
            window.attributes = lp
            sleepOverlay.visibility = View.VISIBLE
        }
    }

    fun wake() {
        val now = System.currentTimeMillis()
        lastWakeTime = now
        lastTouchOrWakeTime = now
        resetIdleTimer()
        if (!isAsleep) return
        isAsleep = false
        Log.i(TAG, "Waking from sleep (screenBrightness = $AWAKE_BRIGHTNESS)")
        runOnUiThread {
            val lp = window.attributes
            lp.screenBrightness = AWAKE_BRIGHTNESS
            window.attributes = lp
            sleepOverlay.visibility = View.GONE
        }
    }

    fun setBrightness(brightness: Float) {
        Log.i(TAG, "Setting screen brightness to $brightness")
        runOnUiThread {
            val lp = window.attributes
            lp.screenBrightness = brightness
            window.attributes = lp
        }
    }

    fun setNightSchedule(startHHMM: String, endHHMM: String) {
        Log.i(TAG, "Updating night quiet-hours schedule: $startHHMM to $endHHMM")
        val prefs = getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
        val editor = prefs.edit()
        if (startHHMM.isNotBlank()) {
            editor.putString(KEY_NIGHT_START, startHHMM.trim())
        }
        if (endHHMM.isNotBlank()) {
            editor.putString(KEY_NIGHT_END, endHHMM.trim())
        }
        editor.apply()
        evaluateNightSchedule()
    }

    fun installOtaApk(apkUrl: String) {
        val startUrl = resolveStartUrl(intent)
        val extraOrigins = resolveBridgeOrigins()
        val otaEnabled = resolveOtaEnabled()
        if (!isOtaUrlAllowed(apkUrl, startUrl, extraOrigins, otaEnabled)) {
            Log.w(
                TAG,
                "Blocked unauthorized or disabled OTA APK install request: url=$apkUrl, otaEnabled=$otaEnabled"
            )
            return
        }

        Thread {
            var connection: HttpURLConnection? = null
            try {
                Log.i(TAG, "Starting OTA APK download from: $apkUrl")
                val url = URL(apkUrl)
                connection = (url.openConnection() as HttpURLConnection).apply {
                    connectTimeout = 15000
                    readTimeout = 30000
                    instanceFollowRedirects = true
                    connect()
                }

                val code = connection.responseCode
                if (code != HttpURLConnection.HTTP_OK) {
                    connection.errorStream?.use { it.readBytes() }
                    Log.e(TAG, "OTA APK download failed with HTTP $code")
                    return@Thread
                }

                val apkFile = File(cacheDir, "ember-ota.apk")
                if (apkFile.exists()) {
                    apkFile.delete()
                }

                connection.inputStream.use { input ->
                    apkFile.outputStream().use { output ->
                        input.copyTo(output)
                    }
                }
                Log.i(TAG, "OTA APK downloaded successfully: ${apkFile.length()} bytes")

                val packageInstaller = packageManager.packageInstaller
                val params = PackageInstaller.SessionParams(PackageInstaller.SessionParams.MODE_FULL_INSTALL)
                val sessionId = packageInstaller.createSession(params)
                val session = packageInstaller.openSession(sessionId)

                session.use { s ->
                    apkFile.inputStream().use { fileIn ->
                        s.openWrite("ember-ota", 0, apkFile.length()).use { out ->
                            fileIn.copyTo(out)
                            s.fsync(out)
                        }
                    }

                    val statusIntent = Intent(this, OtaInstallReceiver::class.java).apply {
                        setPackage(packageName)
                    }
                    val mutableFlag = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.S) {
                        PendingIntent.FLAG_MUTABLE
                    } else {
                        0
                    }
                    val flags = PendingIntent.FLAG_UPDATE_CURRENT or mutableFlag
                    val pendingIntent = PendingIntent.getBroadcast(this, sessionId, statusIntent, flags)
                    s.commit(pendingIntent.intentSender)
                    Log.i(TAG, "PackageInstaller session $sessionId committed successfully")
                }
            } catch (e: Exception) {
                Log.e(TAG, "Failed to install OTA APK from $apkUrl", e)
            } finally {
                connection?.disconnect()
            }
        }.start()
    }

    private fun runIfBridgeOriginAllowed(methodName: String, action: () -> Unit) {
        if (!isCurrentBridgeOriginAllowed()) {
            Log.w(TAG, "Blocked EmberBridge.$methodName() from untrusted cached origin ($currentPageUrl)")
            return
        }
        mainHandler.post {
            if (isFinishing || isDestroyed) return@post
            val liveTopUrl = if (::webView.isInitialized) webView.url ?: currentPageUrl else currentPageUrl
            if (!isBridgeOriginAllowed(liveTopUrl, resolveStartUrl(intent), resolveBridgeOrigins())) {
                Log.w(TAG, "Blocked EmberBridge.$methodName() after UI-thread origin check ($liveTopUrl)")
                return@post
            }
            action()
        }
    }

    /**
     * JavaScript Bridge interface exposed to Hearth via window.EmberBridge.
     */
    inner class EmberBridge {
        @JavascriptInterface
        fun sleep() {
            if (!isCurrentBridgeOriginAllowed()) {
                Log.w(TAG, "Blocked EmberBridge.sleep() from untrusted origin ($currentPageUrl)")
                return
            }
            runIfBridgeOriginAllowed("sleep") {
                Log.i(TAG, "EmberBridge.sleep() invoked from JS")
                this@MainActivity.sleep()
            }
        }

        @JavascriptInterface
        fun wake() {
            if (!isCurrentBridgeOriginAllowed()) {
                Log.w(TAG, "Blocked EmberBridge.wake() from untrusted origin ($currentPageUrl)")
                return
            }
            runIfBridgeOriginAllowed("wake") {
                Log.i(TAG, "EmberBridge.wake() invoked from JS")
                this@MainActivity.wake()
            }
        }

        @JavascriptInterface
        fun setBrightness(brightness: Float) {
            if (!isCurrentBridgeOriginAllowed()) {
                Log.w(TAG, "Blocked EmberBridge.setBrightness() from untrusted origin ($currentPageUrl)")
                return
            }
            runIfBridgeOriginAllowed("setBrightness") {
                Log.i(TAG, "EmberBridge.setBrightness($brightness) invoked from JS")
                this@MainActivity.setBrightness(brightness)
            }
        }

        @JavascriptInterface
        fun setNightSchedule(startHHMM: String, endHHMM: String) {
            if (!isCurrentBridgeOriginAllowed()) {
                Log.w(TAG, "Blocked EmberBridge.setNightSchedule() from untrusted origin ($currentPageUrl)")
                return
            }
            runIfBridgeOriginAllowed("setNightSchedule") {
                Log.i(TAG, "EmberBridge.setNightSchedule($startHHMM, $endHHMM) invoked from JS")
                this@MainActivity.setNightSchedule(startHHMM, endHHMM)
            }
        }

        @JavascriptInterface
        fun installOta(apkUrl: String) {
            if (!isCurrentBridgeOriginAllowed()) {
                Log.w(TAG, "Blocked EmberBridge.installOta() from untrusted origin ($currentPageUrl)")
                return
            }
            runIfBridgeOriginAllowed("installOta") {
                Log.i(TAG, "EmberBridge.installOta($apkUrl) invoked from JS")
                this@MainActivity.installOtaApk(apkUrl)
            }
        }

        @JavascriptInterface
        fun syncGooglePhotosAlbum(albumUrl: String) {
            if (!isCurrentBridgeOriginAllowed()) {
                Log.w(TAG, "Blocked EmberBridge.syncGooglePhotosAlbum() from untrusted origin ($currentPageUrl)")
                return
            }
            if (!isGooglePhotosAlbumUrlAllowed(albumUrl)) {
                Log.w(TAG, "Blocked EmberBridge.syncGooglePhotosAlbum() for non-Google-Photos URL: $albumUrl")
                return
            }
            runIfBridgeOriginAllowed("syncGooglePhotosAlbum") {
                Log.i(TAG, "EmberBridge.syncGooglePhotosAlbum($albumUrl) invoked from JS")
                this@MainActivity.startGooglePhotosSync(albumUrl.trim(), interactive = false)
            }
        }

        @JavascriptInterface
        fun connectGooglePhotos(albumUrl: String) {
            if (!isCurrentBridgeOriginAllowed()) {
                Log.w(TAG, "Blocked EmberBridge.connectGooglePhotos() from untrusted origin ($currentPageUrl)")
                return
            }
            if (!isGooglePhotosAlbumUrlAllowed(albumUrl)) {
                Log.w(TAG, "Blocked EmberBridge.connectGooglePhotos() for non-Google-Photos URL: $albumUrl")
                return
            }
            runIfBridgeOriginAllowed("connectGooglePhotos") {
                Log.i(TAG, "EmberBridge.connectGooglePhotos($albumUrl) invoked from JS")
                this@MainActivity.startGooglePhotosSync(albumUrl.trim(), interactive = true)
            }
        }
    }

    fun cleanupPhotosSync() {
        activeSyncTimeoutRunnable?.let { mainHandler.removeCallbacks(it) }
        activeSyncTimeoutRunnable = null
        try {
            activeAuthDialog?.setOnDismissListener(null)
            if (activeAuthDialog?.isShowing == true) {
                activeAuthDialog?.dismiss()
            }
        } catch (_: Exception) {
        } finally {
            activeAuthDialog = null
        }
        try {
            activeSyncWebView?.stopLoading()
            activeSyncWebView?.destroy()
        } catch (_: Exception) {
        } finally {
            activeSyncWebView = null
            isPhotosSyncInFlight = false
        }
    }

    @SuppressLint("SetJavaScriptEnabled")
    fun startGooglePhotosSync(albumUrl: String, interactive: Boolean) {
        if (!isGooglePhotosAlbumUrlAllowed(albumUrl)) return
        mainHandler.post {
            if (isFinishing || isDestroyed) return@post
            if (isPhotosSyncInFlight) {
                if (!interactive) {
                    Log.i(TAG, "Skipping duplicate background Google Photos sync while sync is already in flight")
                    return@post
                }
                cleanupPhotosSync()
            }
            isPhotosSyncInFlight = true
            val cookieManager = CookieManager.getInstance()
            cookieManager.setAcceptCookie(true)
            val syncWebView = WebView(this).apply {
                settings.javaScriptEnabled = true
                settings.domStorageEnabled = true
                settings.databaseEnabled = true
                settings.useWideViewPort = true
                settings.loadWithOverviewMode = true
            }
            activeSyncWebView = syncWebView
            cookieManager.setAcceptThirdPartyCookies(syncWebView, true)

            var syncDone = false

            if (interactive) {
                val dialog = AlertDialog.Builder(this)
                    .setTitle("Google Photos Account Sync")
                    .setView(syncWebView)
                    .setNegativeButton("Close") { dlg, _ ->
                        dlg.dismiss()
                    }
                    .create()
                dialog.setOnDismissListener {
                    syncDone = true
                    cleanupPhotosSync()
                }
                activeAuthDialog = dialog
                dialog.show()
            } else {
                val timeoutTask = Runnable {
                    if (!syncDone) {
                        syncDone = true
                        Log.w(TAG, "Background Google Photos sync timed out after ${PHOTOS_SYNC_TIMEOUT_MS}ms")
                        dispatchPhotosSyncPayload(
                            JSONObject()
                                .put("ok", false)
                                .put("error", "timeout")
                                .put("photos", JSONArray())
                                .toString()
                        )
                        cleanupPhotosSync()
                    }
                }
                activeSyncTimeoutRunnable = timeoutTask
                mainHandler.postDelayed(timeoutTask, PHOTOS_SYNC_TIMEOUT_MS)
            }

            syncWebView.webViewClient = object : WebViewClient() {
                override fun onPageFinished(view: WebView?, url: String?) {
                    super.onPageFinished(view, url)
                    if (syncDone) return
                    val current = url ?: ""
                    if (current.contains("accounts.google.com") && !interactive) {
                        syncDone = true
                        dispatchPhotosSyncPayload(
                            JSONObject()
                                .put("ok", false)
                                .put("needs_auth", true)
                                .put("photos", JSONArray())
                                .toString()
                        )
                        cleanupPhotosSync()
                        return
                    }
                    view?.evaluateJavascript(
                        "(function(){return document.documentElement ? document.documentElement.outerHTML : '';})()"
                    ) { rawJsonHtml ->
                        if (syncDone) return@evaluateJavascript
                        val urls = extractGooglePhotosUrls(rawJsonHtml)
                        if (urls.isNotEmpty()) {
                            syncDone = true
                            cookieManager.flush()
                            val arr = JSONArray()
                            urls.forEach { arr.put(it) }
                            val payload = JSONObject()
                                .put("ok", true)
                                .put("source", "ember-android")
                                .put("photos", arr)
                                .toString()
                            dispatchPhotosSyncPayload(payload)
                            cleanupPhotosSync()
                        }
                    }
                }
            }
            syncWebView.loadUrl(albumUrl)
        }
    }

    private fun dispatchPhotosSyncPayload(jsonPayload: String) {
        mainHandler.post {
            if (!::webView.isInitialized || isFinishing || isDestroyed) return@post
            val escaped = JSONObject.quote(jsonPayload)
            val script = """
                (function() {
                    var p = $escaped;
                    if (typeof window.onEmberPhotosSynced === 'function') {
                        window.onEmberPhotosSynced(p);
                    } else {
                        var frames = document.querySelectorAll('iframe');
                        for (var i = 0; i < frames.length; i++) {
                            try {
                                if (frames[i].contentWindow && typeof frames[i].contentWindow.onEmberPhotosSynced === 'function') {
                                    frames[i].contentWindow.onEmberPhotosSynced(p);
                                }
                            } catch (e) {}
                        }
                    }
                })();
            """.trimIndent()
            webView.evaluateJavascript(script, null)
        }
    }

    fun startNightScheduleCheck() {
        mainHandler.removeCallbacks(nightScheduleRunnable)
        evaluateNightSchedule()
        mainHandler.postDelayed(nightScheduleRunnable, NIGHT_CHECK_INTERVAL_MS)
    }

    fun stopNightScheduleCheck() {
        mainHandler.removeCallbacks(nightScheduleRunnable)
    }

    fun evaluateNightSchedule() {
        val cal = Calendar.getInstance()
        evaluateNightSchedule(cal.get(Calendar.HOUR_OF_DAY), cal.get(Calendar.MINUTE))
    }

    fun evaluateNightSchedule(hour: Int, minute: Int) {
        if (isFinishing || isDestroyed) return
        val (startMin, endMin) = resolveNightSchedule()
        val now = System.currentTimeMillis()
        val decision = decideNightSchedule(
            hour = hour,
            minute = minute,
            startMinutes = startMin,
            endMinutes = endMin,
            wasInNightWindow = wasInNightWindow,
            isAsleep = isAsleep,
            nowMs = now,
            lastTouchOrWakeTimeMs = lastTouchOrWakeTime,
            idleTimeoutMs = IDLE_TIMEOUT_MS
        )

        when (decision.action) {
            NightScheduleAction.WAKE -> {
                Log.i(TAG, "Exiting night quiet hours at %02d:%02d - waking".format(hour, minute))
                wake()
            }
            NightScheduleAction.SLEEP -> {
                Log.i(
                    TAG,
                    "Night quiet hours active at %02d:%02d and idle timeout elapsed (%d ms) - sleeping"
                        .format(hour, minute, now - lastTouchOrWakeTime)
                )
                sleep()
            }
            NightScheduleAction.NONE -> {}
        }

        wasInNightWindow = decision.inNightWindow
    }

    fun resolveNightSchedule(): Pair<Int, Int> {
        val prefs = getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
        val defaultStartMin = 0
        val defaultEndMin = 6 * 60 + 30
        val startStr = prefs.getString(KEY_NIGHT_START, DEFAULT_NIGHT_START) ?: DEFAULT_NIGHT_START
        val endStr = prefs.getString(KEY_NIGHT_END, DEFAULT_NIGHT_END) ?: DEFAULT_NIGHT_END
        val startMin = parseTimeMinutes(startStr, defaultStartMin)
        val endMin = parseTimeMinutes(endStr, defaultEndMin)
        return Pair(startMin, endMin)
    }

    fun handleNightScheduleIntent(intent: Intent?) {
        if (intent == null) return
        val start = intent.getStringExtra(EXTRA_NIGHT_START)
        val end = intent.getStringExtra(EXTRA_NIGHT_END)
        if (!start.isNullOrBlank() || !end.isNullOrBlank()) {
            val prefs = getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
            val editor = prefs.edit()
            if (!start.isNullOrBlank()) {
                editor.putString(KEY_NIGHT_START, start)
            }
            if (!end.isNullOrBlank()) {
                editor.putString(KEY_NIGHT_END, end)
            }
            editor.apply()
        }
    }

    fun handleAmbientLuma(avgLuma: Double) {
        val now = System.currentTimeMillis()
        if (shouldSleepOnAmbientLuma(
                avgLuma = avgLuma,
                isAsleep = isAsleep,
                nowMs = now,
                lastWakeTimeMs = lastWakeTime,
                threshold = DARKNESS_LUMA_THRESHOLD,
                gracePeriodMs = WAKE_GRACE_PERIOD_MS
            )
        ) {
            Log.i(TAG, "Ambient darkness detected (luma=$avgLuma < $DARKNESS_LUMA_THRESHOLD) - sleeping")
            sleep()
        }
    }

    fun resetIdleTimer() {
        mainHandler.removeCallbacks(idleRunnable)
        mainHandler.postDelayed(idleRunnable, IDLE_TIMEOUT_MS)
    }

    fun resolveStartUrl(intent: Intent?): String {
        val intentUrl = intent?.getStringExtra(EXTRA_START_URL)
        val prefs = getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
        val savedUrl = prefs.getString(KEY_START_URL, null)
        val (resolvedUrl, urlToPersist) = resolveStartUrlValue(intentUrl, savedUrl, DEFAULT_START_URL)
        if (urlToPersist != null) {
            saveStartUrl(urlToPersist)
        }
        return resolvedUrl
    }

    fun saveStartUrl(url: String) {
        val prefs = getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
        prefs.edit().putString(KEY_START_URL, url).apply()
    }

    fun resolveBridgeOrigins(): String? {
        val prefs = getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
        return prefs.getString(KEY_BRIDGE_ORIGINS, null)
    }

    fun resolveOtaEnabled(): Boolean {
        val prefs = getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
        return prefs.getBoolean(KEY_OTA_ENABLED, DEFAULT_OTA_ENABLED)
    }

    fun resolveAllowMixedContent(): Boolean {
        val prefs = getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
        return prefs.getBoolean(KEY_ALLOW_MIXED_CONTENT, DEFAULT_ALLOW_MIXED_CONTENT)
    }

    fun handleSecurityConfigIntent(intent: Intent?) {
        if (intent == null) return
        val prefs = getSharedPreferences(PREFS_NAME, Context.MODE_PRIVATE)
        val editor = prefs.edit()
        var modified = false

        if (intent.hasExtra(EXTRA_BRIDGE_ORIGINS)) {
            val origins = intent.getStringExtra(EXTRA_BRIDGE_ORIGINS)
            if (origins != null) {
                editor.putString(KEY_BRIDGE_ORIGINS, origins.trim())
                modified = true
            }
        }

        if (intent.hasExtra(EXTRA_OTA_ENABLED)) {
            val otaVal = try {
                intent.getBooleanExtra(EXTRA_OTA_ENABLED, DEFAULT_OTA_ENABLED)
            } catch (e: Exception) {
                intent.getStringExtra(EXTRA_OTA_ENABLED)?.trim()?.toBoolean() ?: DEFAULT_OTA_ENABLED
            }
            editor.putBoolean(KEY_OTA_ENABLED, otaVal)
            modified = true
        }

        if (intent.hasExtra(EXTRA_ALLOW_MIXED_CONTENT)) {
            val mixedVal = try {
                intent.getBooleanExtra(EXTRA_ALLOW_MIXED_CONTENT, DEFAULT_ALLOW_MIXED_CONTENT)
            } catch (e: Exception) {
                intent.getStringExtra(EXTRA_ALLOW_MIXED_CONTENT)?.trim()?.toBoolean() ?: DEFAULT_ALLOW_MIXED_CONTENT
            }
            editor.putBoolean(KEY_ALLOW_MIXED_CONTENT, mixedVal)
            modified = true
        }

        if (modified) {
            editor.apply()
        }
    }

    fun isCurrentBridgeOriginAllowed(): Boolean {
        val current = currentPageUrl ?: if (Looper.myLooper() == Looper.getMainLooper() && ::webView.isInitialized) webView.url else null
        val startUrl = resolveStartUrl(intent)
        val extraOrigins = resolveBridgeOrigins()
        return isBridgeOriginAllowed(current, startUrl, extraOrigins)
    }

    fun loadTargetUrl(url: String) {
        Log.i(TAG, "Loading target URL: $url")
        currentPageUrl = url
        webView.loadUrl(url)
    }

    fun scheduleReload() {
        if (reloadPending) return
        reloadPending = true
        Log.w(TAG, "Scheduling WebView reload in ${RETRY_DELAY_MS}ms")
        mainHandler.removeCallbacks(reloadRunnable)
        mainHandler.postDelayed(reloadRunnable, RETRY_DELAY_MS)
    }

    fun startMotionMonitor() {
        try {
            if (!motionLumaMonitor.running) {
                motionLumaMonitor.start()
            }
        } catch (e: Exception) {
            Log.e(TAG, "Failed to start MotionLumaMonitor", e)
        }
    }

    fun stopMotionMonitor() {
        try {
            if (motionLumaMonitor.running) {
                motionLumaMonitor.stop()
            }
        } catch (e: Exception) {
            Log.e(TAG, "Failed to stop MotionLumaMonitor", e)
        }
    }

    private fun setupViews() {
        val rootLayout = FrameLayout(this).apply {
            layoutParams = FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT,
                FrameLayout.LayoutParams.MATCH_PARENT
            )
        }

        webView = WebView(this).apply {
            layoutParams = FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT,
                FrameLayout.LayoutParams.MATCH_PARENT
            )
        }

        sleepOverlay = View(this).apply {
            layoutParams = FrameLayout.LayoutParams(
                FrameLayout.LayoutParams.MATCH_PARENT,
                FrameLayout.LayoutParams.MATCH_PARENT
            )
            setBackgroundColor(Color.BLACK)
            visibility = View.GONE
            isClickable = true
            isFocusable = true
            setOnClickListener {
                wake()
            }
        }

        rootLayout.addView(webView)
        rootLayout.addView(sleepOverlay)
        setContentView(rootLayout)
    }

    @SuppressLint("SetJavaScriptEnabled")
    private fun configureWebView() {
        val allowMixed = resolveAllowMixedContent()
        webView.settings.apply {
            javaScriptEnabled = true
            domStorageEnabled = true
            databaseEnabled = true
            mediaPlaybackRequiresUserGesture = false
            mixedContentMode = if (allowMixed) {
                WebSettings.MIXED_CONTENT_ALWAYS_ALLOW
            } else {
                WebSettings.MIXED_CONTENT_NEVER_ALLOW
            }
            setSupportZoom(false)
            builtInZoomControls = false
            displayZoomControls = false
            useWideViewPort = true
            loadWithOverviewMode = true
            cacheMode = WebSettings.LOAD_DEFAULT
        }

        val cookieManager = CookieManager.getInstance()
        cookieManager.setAcceptCookie(true)
        cookieManager.setAcceptThirdPartyCookies(webView, true)

        webView.addJavascriptInterface(EmberBridge(), "EmberBridge")

        webView.webChromeClient = object : WebChromeClient() {
            override fun onPermissionRequest(request: PermissionRequest) {
                Log.i(TAG, "Granting WebChromeClient permission: ${request.resources.joinToString()}")
                request.grant(request.resources)
            }
        }

        webView.webViewClient = object : WebViewClient() {
            override fun onPageStarted(view: WebView?, url: String?, favicon: Bitmap?) {
                super.onPageStarted(view, url, favicon)
                if (!url.isNullOrBlank()) {
                    currentPageUrl = url
                }
            }

            override fun onPageFinished(view: WebView?, url: String?) {
                super.onPageFinished(view, url)
                reloadPending = false
                if (!url.isNullOrBlank()) {
                    currentPageUrl = url
                }
            }

            override fun doUpdateVisitedHistory(view: WebView?, url: String?, isReload: Boolean) {
                super.doUpdateVisitedHistory(view, url, isReload)
                if (!url.isNullOrBlank()) {
                    currentPageUrl = url
                }
            }

            override fun onReceivedError(
                view: WebView?,
                request: WebResourceRequest?,
                error: WebResourceError?
            ) {
                super.onReceivedError(view, request, error)
                if (request?.isForMainFrame == true) {
                    Log.w(TAG, "WebView main frame error: ${error?.description}")
                    scheduleReload()
                }
            }

            @Suppress("DEPRECATION")
            override fun onReceivedError(
                view: WebView?,
                errorCode: Int,
                description: String?,
                failingUrl: String?
            ) {
                super.onReceivedError(view, errorCode, description, failingUrl)
                Log.w(TAG, "WebView error ($errorCode): $description")
                scheduleReload()
            }

            override fun onReceivedHttpError(
                view: WebView?,
                request: WebResourceRequest?,
                errorResponse: WebResourceResponse?
            ) {
                super.onReceivedHttpError(view, request, errorResponse)
                if (request?.isForMainFrame == true && (errorResponse?.statusCode ?: 0) >= 400) {
                    Log.w(TAG, "WebView HTTP error: ${errorResponse?.statusCode}")
                    scheduleReload()
                }
            }

        }
    }

    private fun checkAndRequestPermissions() {
        val needed = mutableListOf<String>()
        if (checkSelfPermission(Manifest.permission.CAMERA) != PackageManager.PERMISSION_GRANTED) {
            needed.add(Manifest.permission.CAMERA)
        }
        if (checkSelfPermission(Manifest.permission.RECORD_AUDIO) != PackageManager.PERMISSION_GRANTED) {
            needed.add(Manifest.permission.RECORD_AUDIO)
        }
        if (needed.isNotEmpty()) {
            requestPermissions(needed.toTypedArray(), PERMISSION_REQUEST_CODE)
        } else {
            startMotionMonitor()
        }
    }

    override fun onRequestPermissionsResult(
        requestCode: Int,
        permissions: Array<out String>,
        grantResults: IntArray
    ) {
        super.onRequestPermissionsResult(requestCode, permissions, grantResults)
        if (requestCode == PERMISSION_REQUEST_CODE) {
            if (checkSelfPermission(Manifest.permission.CAMERA) == PackageManager.PERMISSION_GRANTED) {
                startMotionMonitor()
            }
        }
    }

    private fun setImmersiveMode() {
        val decor = window?.peekDecorView() ?: return
        decor.post {
            try {
                if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.R) {
                    window.setDecorFitsSystemWindows(false)
                    decor.windowInsetsController?.let { controller ->
                        controller.hide(WindowInsets.Type.statusBars() or WindowInsets.Type.navigationBars())
                        controller.systemBarsBehavior =
                            WindowInsetsController.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
                    }
                } else {
                    @Suppress("DEPRECATION")
                    decor.systemUiVisibility = (
                        View.SYSTEM_UI_FLAG_IMMERSIVE_STICKY
                            or View.SYSTEM_UI_FLAG_LAYOUT_STABLE
                            or View.SYSTEM_UI_FLAG_LAYOUT_HIDE_NAVIGATION
                            or View.SYSTEM_UI_FLAG_LAYOUT_FULLSCREEN
                            or View.SYSTEM_UI_FLAG_HIDE_NAVIGATION
                            or View.SYSTEM_UI_FLAG_FULLSCREEN
                    )
                }
            } catch (e: Throwable) {
                Log.w(TAG, "Immersive mode setup deferred or unsupported", e)
            }
        }
    }
}


/**
 * Dedicated BroadcastReceiver for PackageInstaller OTA session status callbacks.
 */
class OtaInstallReceiver : BroadcastReceiver() {
    override fun onReceive(context: Context, intent: Intent) {
        val status = intent.getIntExtra(PackageInstaller.EXTRA_STATUS, PackageInstaller.STATUS_FAILURE)
        val message = intent.getStringExtra(PackageInstaller.EXTRA_STATUS_MESSAGE)
        when (status) {
            PackageInstaller.STATUS_PENDING_USER_ACTION -> {
                @Suppress("DEPRECATION")
                val confirmIntent = if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.TIRAMISU) {
                    intent.getParcelableExtra(Intent.EXTRA_INTENT, Intent::class.java)
                } else {
                    intent.getParcelableExtra(Intent.EXTRA_INTENT) as? Intent
                }
                if (confirmIntent != null) {
                    confirmIntent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
                    context.startActivity(confirmIntent)
                }
            }
            PackageInstaller.STATUS_SUCCESS -> {
                Log.i("OtaInstallReceiver", "OTA APK installation succeeded")
            }
            else -> {
                Log.e("OtaInstallReceiver", "OTA APK installation failed ($status): $message")
            }
        }
    }
}
