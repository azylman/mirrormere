package ventures.brock.ember

import android.content.BroadcastReceiver
import android.content.Context
import android.content.Intent
import android.util.Log

/**
 * BroadcastReceiver triggered by android.intent.action.BOOT_COMPLETED.
 * Auto-launches MainActivity on system boot to maintain continuous kiosk operation.
 *
 * Enforces zero Wi-Fi dependencies and lightweight native platform APIs.
 */
class BootReceiver : BroadcastReceiver() {

    companion object {
        private const val TAG = "BootReceiver"
    }

    override fun onReceive(context: Context, intent: Intent) {
        if (intent.action == Intent.ACTION_BOOT_COMPLETED) {
            Log.i(TAG, "ACTION_BOOT_COMPLETED received - auto-starting EMBER MainActivity")

            val launchIntent = context.packageManager?.getLaunchIntentForPackage(context.packageName)
                ?: Intent().apply {
                    setClassName(context.packageName, "ventures.brock.ember.MainActivity")
                }

            launchIntent.addFlags(Intent.FLAG_ACTIVITY_NEW_TASK)
            try {
                context.startActivity(launchIntent)
            } catch (e: Exception) {
                Log.e(TAG, "Failed to launch MainActivity on boot", e)
            }
        }
    }
}
