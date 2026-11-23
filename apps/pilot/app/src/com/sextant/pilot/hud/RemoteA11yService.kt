package com.sextant.pilot.hud

import com.sextant.pilot.R

import android.accessibilityservice.AccessibilityService
import android.content.Intent
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.util.Log
import android.view.accessibility.AccessibilityEvent
import org.json.JSONObject

/**
 * Owns the main-screen control: the accessibility overlay (ball / capsule / card),
 * the edge beam, the daemon SSE feed and the local capture/pass-through socket.
 *
 * The user must enable this service once in Settings › Accessibility. Android does
 * not let an app (or root) silently bind an accessibility service; the module only
 * clears the package's stopped state and opens the settings page when asked.
 */
class RemoteA11yService : AccessibilityService(), HudClient.Listener {
    private val main = Handler(Looper.getMainLooper())
    private var overlay: ControlOverlay? = null
    private var client: HudClient? = null
    private var config: HudConfig? = null
    private var activeToken: String? = null
    private var retry: Runnable? = null
    private var clearStatus: Runnable? = null
    private val controls = HudControls()
    // ACKs run off the SSE thread so a slow loopback POST cannot stall the state
    // stream and make the daemon miss the next request_id.
    private val ackExecutor = java.util.concurrent.Executors.newSingleThreadExecutor { r ->
        Thread(r, "sextant-hud-ack").apply { isDaemon = true }
    }
    private var lastAckId = ""

    private val listener = object : ControlListener {
        override fun shownControls(): TaskControls? = controls
    }

    override fun onServiceConnected() {
        super.onServiceConnected()
        Log.i(TAG, "connected")
        PhoneRegistry.controlListener = listener
        PhoneRegistry.unlocks = { PhoneUnlocks() }
        if (overlay == null) overlay = ControlOverlay(this)
        scheduleProvision(0)
    }

    override fun onAccessibilityEvent(event: AccessibilityEvent?) {
        when (event?.eventType) {
            AccessibilityEvent.TYPE_WINDOWS_CHANGED,
            AccessibilityEvent.TYPE_WINDOW_STATE_CHANGED -> overlay?.onWindowsChanged()
        }
    }

    override fun onInterrupt() {
        overlay?.stopAll()
    }

    override fun onUnbind(intent: Intent?): Boolean {
        cleanup()
        return super.onUnbind(intent)
    }

    override fun onDestroy() {
        cleanup()
        super.onDestroy()
    }

    private fun cleanup() {
        retry?.let(main::removeCallbacks)
        retry = null
        clearStatus?.let(main::removeCallbacks)
        clearStatus = null
        client?.stop()
        client = null
        config = null
        activeToken = null
        if (PhoneRegistry.controlListener === listener) PhoneRegistry.controlListener = null
        overlay?.destroy()
        overlay = null
    }

    /** The daemon may start after the user enabled the service, or rewrite hud.json
     * with a fresh token; retry until a config loads, then keep a client on it. */
    private fun scheduleProvision(delayMs: Long) {
        retry?.let(main::removeCallbacks)
        val r = Runnable { provision() }
        retry = r
        main.postDelayed(r, delayMs)
    }

    private fun provision() {
        retry = null
        val cfg = try {
            HudConfig.load(this)
        } catch (t: Throwable) {
            Log.i(TAG, "waiting for hud.json: ${t.javaClass.simpleName}")
            scheduleProvision(1_000)
            return
        }
        if (cfg.token == activeToken && client != null) return
        client?.stop()
        config = cfg
        activeToken = cfg.token
        try {
            client = HudClient(cfg, this).also { it.start() }
            Log.i(TAG, "provisioned for ${cfg.agent}")
        } catch (t: Throwable) {
            Log.w(TAG, "provision failed: ${t.javaClass.simpleName}", t)
            client = null
            activeToken = null
            scheduleProvision(1_500)
        }
    }

    override fun onState(state: JSONObject) {
        val visible = state.optBoolean("visible", false)
        val paused = state.optBoolean("paused", false)
        val action = state.optString("action", "").trim().ifBlank { "控制中" }
        val updatedAt = state.optLong("updated_at", 0L)
        controls.update(paused, visible, updatedAt)
        // The daemon tells the overlay to step aside through the same state frame
        // it uses for everything else. Hide for a capture, ignore touches while a
        // stroke is injected: both must be applied before the daemon acts, which is
        // why it is done here, synchronously, rather than on the delayed runnable.
        overlay?.setCaptureHiddenForHost(state.optBoolean("capture_hidden", false))
        overlay?.setPassThroughForHost(state.optBoolean("pass_through", false))
        // D28: tell the daemon the instruction has been applied so it does not
        // have to wait out the settle timeout on every capture and every stroke.
        // The ACK runs off the SSE reader thread: posting it inline blocked the
        // stream long enough that a following state frame could be dropped.
        val requestId = state.optString("request_id", "")
        if (requestId.isNotEmpty() && requestId != lastAckId) {
            lastAckId = requestId
            val c = client
            if (c != null) ackExecutor.execute { c.postAck(requestId) }
        }
        main.post {
            if (visible) {
                clearStatus?.let(main::removeCallbacks)
                clearStatus = null
                overlay?.status(getString(R.string.phone_acting, action))
            } else {
                if (clearStatus == null) {
                    val r = Runnable {
                        clearStatus = null
                        overlay?.status(null)
                    }
                    clearStatus = r
                    main.postDelayed(r, STOPPED_LINGER_MS)
                }
            }
        }
    }

    override fun onDisconnected(reason: String) {
        Log.i(TAG, "disconnected: $reason")
        main.post {
            // Only the HTTP client is torn down here. The control socket stays for
            // the service's whole lifetime: rebinding the abstract name races with
            // the kernel releasing it, and the loser gets "Address already in use"
            // and never recovers — every capture then fails with "cannot hide the
            // overlay" even though the overlay itself is fine.
            client?.stop()
            client = null
            activeToken = null
            scheduleProvision(1_500)
        }
    }

    override fun onStopped() {
        Log.i(TAG, "stopped by user")
    }

    // ---- socket host API -----------------------------------------------------

    fun hudPing(): Bundle = Bundle().apply {
        val o = overlay
        putBoolean("visible", o?.isHudVisible() == true)
        putBoolean("hidden", o?.isCaptureHidden() == true)
        putBoolean("touchable", o?.isTouchable() == true)
    }

    fun hudSetCaptureHidden(hide: Boolean): Boolean =
        overlay?.setCaptureHiddenForHost(hide) == true

    fun hudSetPassThrough(on: Boolean): Boolean =
        overlay?.setPassThroughForHost(on) == true

    /** The controls the overlay's 暂停 / 停止 act on. State comes from the daemon SSE. */
    private inner class HudControls : TaskControls {
        @Volatile private var paused = false
        @Volatile private var active = false
        @Volatile private var tick = 0L

        fun update(paused: Boolean, active: Boolean, lifecycle: Long) {
            this.paused = paused
            this.active = active
            this.tick = lifecycle
        }

        override val taskId: Long get() = 1L
        override val lifecycle: Long get() = tick
        override fun isCurrentDisplay(): Boolean = active
        override fun isPaused(): Boolean = paused
        override fun setPaused(paused: Boolean): Boolean {
            if (!active || client == null) return false
            this.paused = paused
            client?.postPause(paused)
            return true
        }
        override fun stop(reason: String): Boolean {
            if (!active || client == null) return false
            client?.postStop()
            return true
        }
        override fun interject(text: String): Boolean = false
    }

    companion object {
        private const val TAG = "SextantHud"
        private const val STOPPED_LINGER_MS = 4_000L
    }
}
