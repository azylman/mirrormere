package ventures.brock.ember

import android.annotation.SuppressLint
import android.content.Context
import android.content.pm.PackageManager
import android.graphics.ImageFormat
import android.hardware.camera2.CameraAccessException
import android.hardware.camera2.CameraCaptureSession
import android.hardware.camera2.CameraCharacteristics
import android.hardware.camera2.CameraDevice
import android.hardware.camera2.CameraManager
import android.hardware.camera2.CaptureRequest
import android.media.Image
import android.media.ImageReader
import android.os.Handler
import android.os.HandlerThread
import android.os.Looper
import android.util.Log
import kotlin.math.abs

/**
 * Camera2-based ambient luminance and frame-delta motion monitor.
 *
 * Samples 160x120 YUV_420_888 frames at ~2 second intervals on a background HandlerThread.
 * Extracts the Y-plane to compute:
 * 1. Average ambient luminance (0..255) for dark/sleep detection.
 * 2. Mean absolute pixel delta against the previous sampled frame for motion wake.
 *
 * Enforces zero Wi-Fi dependencies and lightweight native platform APIs.
 */
class MotionLumaMonitor(
    private val context: Context,
    var listener: Listener? = null,
    var motionThreshold: Double = DEFAULT_MOTION_THRESHOLD,
    var sampleIntervalMs: Long = DEFAULT_SAMPLE_INTERVAL_MS,
    var onMotionDetected: (() -> Unit)? = null,
    var onAmbientLuma: ((Double) -> Unit)? = null
) {

    interface Listener {
        fun onMotionDetected()
        fun onAmbientLuma(avgLuma: Double)
    }

    companion object {
        private const val TAG = "MotionLumaMonitor"
        const val DEFAULT_MOTION_THRESHOLD = 12.0
        const val DEFAULT_SAMPLE_INTERVAL_MS = 2000L
        const val FRAME_WIDTH = 160
        const val FRAME_HEIGHT = 120
    }

    @Volatile
    private var cameraDevice: CameraDevice? = null
    @Volatile
    private var captureSession: CameraCaptureSession? = null
    @Volatile
    private var imageReader: ImageReader? = null
    private var backgroundThread: HandlerThread? = null
    private var backgroundHandler: Handler? = null

    private var previousFramePixels: ByteArray? = null
    private var lastSampleTime: Long = 0L

    @Volatile
    private var isRunning: Boolean = false

    val running: Boolean
        get() = isRunning

    @Synchronized
    fun start() {
        if (isRunning) {
            Log.w(TAG, "MotionLumaMonitor is already running")
            return
        }

        if (context.checkSelfPermission(android.Manifest.permission.CAMERA) != PackageManager.PERMISSION_GRANTED) {
            Log.w(TAG, "android.permission.CAMERA not granted; cannot start camera monitor")
            return
        }

        startBackgroundThread()

        val cameraManager = context.getSystemService(Context.CAMERA_SERVICE) as? CameraManager
        if (cameraManager == null) {
            Log.e(TAG, "CameraManager not available")
            stopBackgroundThread()
            return
        }

        val cameraId = findCameraId(cameraManager)
        if (cameraId == null) {
            Log.w(TAG, "No suitable camera found (neither front nor fallback)")
            stopBackgroundThread()
            return
        }

        setupImageReader()
        openCamera(cameraManager, cameraId)
        isRunning = true
        Log.i(TAG, "MotionLumaMonitor started on camera $cameraId")
    }

    @Synchronized
    fun stop() {
        if (!isRunning) return
        isRunning = false

        try {
            captureSession?.stopRepeating()
            captureSession?.close()
        } catch (e: Exception) {
            Log.w(TAG, "Error closing capture session", e)
        } finally {
            captureSession = null
        }

        try {
            cameraDevice?.close()
        } catch (e: Exception) {
            Log.w(TAG, "Error closing camera device", e)
        } finally {
            cameraDevice = null
        }

        try {
            imageReader?.close()
        } catch (e: Exception) {
            Log.w(TAG, "Error closing ImageReader", e)
        } finally {
            imageReader = null
        }

        stopBackgroundThread()
        previousFramePixels = null
        lastSampleTime = 0L
        Log.i(TAG, "MotionLumaMonitor stopped")
    }

    private fun findCameraId(cameraManager: CameraManager): String? {
        try {
            val cameraIds = cameraManager.cameraIdList
            if (cameraIds.isEmpty()) return null

            // Prioritize front-facing camera for kiosk presence detection
            for (id in cameraIds) {
                val characteristics = cameraManager.getCameraCharacteristics(id)
                val facing = characteristics.get(CameraCharacteristics.LENS_FACING)
                if (facing == CameraCharacteristics.LENS_FACING_FRONT) {
                    return id
                }
            }

            // Fallback to first available camera
            Log.i(TAG, "Front camera not found, falling back to camera ${cameraIds[0]}")
            return cameraIds[0]
        } catch (e: CameraAccessException) {
            Log.e(TAG, "Failed to inspect camera characteristics", e)
            return null
        }
    }

    private fun setupImageReader() {
        val reader = ImageReader.newInstance(
            FRAME_WIDTH,
            FRAME_HEIGHT,
            ImageFormat.YUV_420_888,
            2
        )
        reader.setOnImageAvailableListener({ r ->
            val image = try {
                r.acquireLatestImage()
            } catch (e: Exception) {
                null
            } ?: return@setOnImageAvailableListener

            val now = System.currentTimeMillis()
            if (now - lastSampleTime < sampleIntervalMs) {
                image.close()
                return@setOnImageAvailableListener
            }
            lastSampleTime = now

            try {
                processImage(image)
            } catch (e: Exception) {
                Log.e(TAG, "Error processing camera frame", e)
            } finally {
                image.close()
            }
        }, backgroundHandler)

        imageReader = reader
    }

    @SuppressLint("MissingPermission")
    private fun openCamera(cameraManager: CameraManager, cameraId: String) {
        try {
            cameraManager.openCamera(cameraId, object : CameraDevice.StateCallback() {
                override fun onOpened(camera: CameraDevice) {
                    synchronized(this@MotionLumaMonitor) {
                        if (!isRunning || imageReader == null) {
                            camera.close()
                            return
                        }
                        cameraDevice = camera
                        createCaptureSession(camera)
                    }
                }

                override fun onDisconnected(camera: CameraDevice) {
                    Log.w(TAG, "Camera disconnected")
                    synchronized(this@MotionLumaMonitor) {
                        camera.close()
                        if (cameraDevice == camera) {
                            cameraDevice = null
                        }
                    }
                }

                override fun onError(camera: CameraDevice, error: Int) {
                    Log.e(TAG, "Camera device error: $error")
                    synchronized(this@MotionLumaMonitor) {
                        camera.close()
                        if (cameraDevice == camera) {
                            cameraDevice = null
                        }
                    }
                }
            }, backgroundHandler)
        } catch (e: Exception) {
            Log.e(TAG, "Failed to open camera $cameraId", e)
        }
    }

    private fun createCaptureSession(camera: CameraDevice) {
        val reader = imageReader ?: return
        val surface = reader.surface

        try {
            @Suppress("DEPRECATION")
            camera.createCaptureSession(
                listOf(surface),
                object : CameraCaptureSession.StateCallback() {
                    override fun onConfigured(session: CameraCaptureSession) {
                        val surfaceTarget: android.view.Surface
                        synchronized(this@MotionLumaMonitor) {
                            val activeReader = imageReader
                            if (!isRunning || cameraDevice == null || activeReader == null) {
                                session.close()
                                return
                            }
                            captureSession = session
                            surfaceTarget = activeReader.surface
                        }
                        try {
                            val requestBuilder = camera.createCaptureRequest(CameraDevice.TEMPLATE_PREVIEW).apply {
                                addTarget(surfaceTarget)
                                set(CaptureRequest.CONTROL_AF_MODE, CaptureRequest.CONTROL_AF_MODE_OFF)
                            }
                            session.setRepeatingRequest(requestBuilder.build(), null, backgroundHandler)
                        } catch (e: Exception) {
                            Log.e(TAG, "Failed to set repeating preview request", e)
                        }
                    }

                    override fun onConfigureFailed(session: CameraCaptureSession) {
                        Log.e(TAG, "Camera capture session configuration failed")
                    }
                },
                backgroundHandler
            )
        } catch (e: Exception) {
            Log.e(TAG, "Failed to create camera capture session", e)
        }
    }

    private fun processImage(image: Image) {
        val planes = image.planes
        if (planes.isEmpty()) return
        val yPlane = planes[0] ?: return

        val buffer = yPlane.buffer
        val width = image.width
        val height = image.height
        val rowStride = yPlane.rowStride
        val pixelStride = yPlane.pixelStride

        var sumLuma = 0.0
        var sumDelta = 0.0
        val totalPixels = width * height
        val currentPixels = ByteArray(totalPixels)
        var pixelIndex = 0

        val prev = previousFramePixels

        for (row in 0 until height) {
            val rowOffset = row * rowStride
            for (col in 0 until width) {
                val bufferIndex = rowOffset + col * pixelStride
                val luma = buffer.get(bufferIndex).toInt() and 0xFF
                currentPixels[pixelIndex] = luma.toByte()
                sumLuma += luma

                if (prev != null) {
                    val prevLuma = prev[pixelIndex].toInt() and 0xFF
                    sumDelta += abs(luma - prevLuma)
                }
                pixelIndex++
            }
        }

        val avgLuma = sumLuma / totalPixels
        previousFramePixels = currentPixels

        dispatchAmbientLuma(avgLuma)

        if (prev != null) {
            val meanDelta = sumDelta / totalPixels
            if (meanDelta >= motionThreshold) {
                dispatchMotionDetected()
            }
        }
    }

    private fun dispatchAmbientLuma(avgLuma: Double) {
        postToMain {
            listener?.onAmbientLuma(avgLuma)
            onAmbientLuma?.invoke(avgLuma)
        }
    }

    private fun dispatchMotionDetected() {
        postToMain {
            listener?.onMotionDetected()
            onMotionDetected?.invoke()
        }
    }

    private fun postToMain(action: () -> Unit) {
        try {
            val mainLooper = Looper.getMainLooper()
            if (mainLooper != null && Looper.myLooper() != mainLooper) {
                Handler(mainLooper).post(action)
            } else {
                action()
            }
        } catch (_: Exception) {
            action()
        }
    }

    private fun startBackgroundThread() {
        backgroundThread = HandlerThread("MotionLumaThread").apply {
            start()
            backgroundHandler = Handler(looper)
        }
    }

    private fun stopBackgroundThread() {
        backgroundThread?.quitSafely()
        try {
            backgroundThread?.join(1000)
            backgroundThread = null
            backgroundHandler = null
        } catch (e: InterruptedException) {
            Log.e(TAG, "Interrupted while stopping background thread", e)
        }
    }
}
