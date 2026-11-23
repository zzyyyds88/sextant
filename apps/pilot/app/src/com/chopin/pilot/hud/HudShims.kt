// Minimal contracts the reference overlay code expects. chopin-pilot does not implement the
// reference app's confirmation, steering or background-preview features; these types keep the
// unmodified overlay source compiling and are inert at runtime.

package com.chopin.pilot.hud

import android.app.Activity
import android.content.Context
import android.graphics.Bitmap
import android.graphics.BitmapFactory
import android.graphics.Color
import android.os.Bundle
import android.os.Handler
import android.os.Looper
import android.view.View
import android.widget.TextView
import java.util.concurrent.CountDownLatch
import java.util.concurrent.TimeUnit
import java.util.concurrent.atomic.AtomicBoolean

internal data class PhoneUnlocks(
    val systemPages: Boolean = false,
    val secretFields: Boolean = false,
    val ownApp: Boolean = false,
) {
    val any: Boolean get() = systemPages || secretFields || ownApp
}

internal interface TaskControls {
    val taskId: Long
    val lifecycle: Long
    fun isCurrentDisplay(): Boolean = false
    fun isPaused(): Boolean
    fun setPaused(paused: Boolean): Boolean
    fun stop(reason: String): Boolean
    fun interject(text: String): Boolean
}

internal interface ControlListener {
    fun shownControls(): TaskControls? = null
    fun onStopAllRequested(reason: String, source: StopSource = StopSource.ENVIRONMENT) {}
    fun onAccessibilityChanged(connected: Boolean) {}
}

internal enum class StopSource { ENVIRONMENT, OVERLAY, USER }

internal interface UserGate {
    fun confirm(request: ConfirmRequest): Decision
    fun handoff(reason: String, timeoutMs: Long): HandoffResult
    fun handoff(reason: String, timeoutMs: Long, cause: HandoffCause?): HandoffResult = handoff(reason, timeoutMs)
    fun status(text: String?)
    fun status(text: String?, ifCurrent: (() -> Unit) -> Boolean) {
        if (ifCurrent {}) status(text)
    }
    fun clearStatus(ifCurrent: (() -> Unit) -> Unit) = ifCurrent { status(null) }
    fun close(ifCurrent: (() -> Unit) -> Unit = { it() }) = ifCurrent { status(null) }
}

internal data class ConfirmRequest(
    val sentence: String,
    val appLabel: String? = null,
    val packageName: String? = null,
    val firstInApp: Boolean = false,
    val timeoutMs: Long = 30_000,
    val offerTaskWaiver: Boolean = false,
    val visualScope: VisualScope? = null,
)

internal class VisualScope

internal enum class Decision { ALLOW_ONCE, ALLOW_APP_FOR_TASK, ALLOW_ALL_FOR_TASK, ALLOW_VISUAL_FOR_TASK, DENY, STOP, UNAVAILABLE }

internal enum class HandoffResult { CONTINUE, STOP, TIMEOUT }

internal enum class HandoffCause { SECRET, SYSTEM, DENY_LIST }

internal object PhoneRegistry {
    @Volatile var promptGate: UserGate? = null
    @Volatile var notificationGate: UserGate? = null
    @Volatile var controlListener: ControlListener? = null
    @Volatile var unlocks: () -> PhoneUnlocks = { PhoneUnlocks() }
}

internal class PromptPresentation(
    val live: Boolean = false,
    val ticket: Ticket? = null,
) {
    internal class Ticket {
        fun stop() {}
    }

    fun markShown(): Boolean = true
    fun retire() {}
}

internal interface PhonePromptPresenter {
    fun showPrompt(presentation: PromptPresentation): (() -> Unit)?
}

internal object PhonePromptUi {
    fun card(context: Context, presentation: PromptPresentation): View = TextView(context).apply {
        text = presentation.toString()
    }

    fun handoffCause(context: Context, cause: HandoffCause): String = when (cause) {
        HandoffCause.SECRET -> "密码需要你来输入"
        HandoffCause.SYSTEM -> "系统设置需要你来操作"
        HandoffCause.DENY_LIST -> "这个应用需要你来操作"
    }
}

internal class LiveTask(val id: Long, val app: String? = null)

internal class BackgroundPreview {
    enum class ConsumerKind { OVERLAY }
    fun addConsumer(owner: Any, taskId: Long, kind: ConsumerKind, onFrame: (Map<String, Any?>) -> Unit) {}
    fun removeConsumer(owner: Any) {}
}

internal class PhoneControl private constructor() {
    val preview = BackgroundPreview()
    fun liveTask(): LiveTask? = null

    companion object {
        @Volatile private var instance: PhoneControl? = null
        fun peek(): PhoneControl? = instance
    }
}

internal object AppStrings {
    /**
     * Resolves a string, formatting only when arguments were actually supplied.
     *
     * `getString(id, *emptyArray)` still runs the formatter, so a resource whose
     * text contains `%1$s` throws MissingFormatArgumentException — and the caller
     * here passes no arguments on purpose when it only wants the format's prefix
     * (「正在操作：%1$s」 → 「正在操作：」). That exception was thrown on the UI
     * thread inside the overlay's status callback and crashed the whole app in a
     * loop every time a session started, so the pill never appeared at all.
     */
    fun get(context: Context, id: Int, vararg args: Any): String =
        if (args.isEmpty()) context.getString(id) else context.getString(id, *args)

    fun plural(context: Context, id: Int, quantity: Int, vararg args: Any): String =
        if (args.isEmpty()) context.resources.getQuantityString(id, quantity)
        else context.resources.getQuantityString(id, quantity, *args)
}

internal class MainThreadCall<T>(private val block: () -> T) {
    private val latch = CountDownLatch(1)
    @Volatile private var result: T? = null
    fun run() {
        try { result = block() } finally { latch.countDown() }
    }
    fun await(timeoutMs: Long): T? {
        latch.await(timeoutMs, TimeUnit.MILLISECONDS)
        return result
    }
}

internal class OverlayPreviewDecoder(private val onBitmap: (Bitmap?) -> Unit) {
    private val main = Handler(Looper.getMainLooper())
    fun offer(bytes: ByteArray) {
        val bitmap = BitmapFactory.decodeByteArray(bytes, 0, bytes.size)
        main.post { onBitmap(bitmap) }
    }
    fun clear() { main.post { onBitmap(null) } }
}

internal class CaptureVisibility {
    @Volatile var hidden: Boolean = false
        private set
    fun update(hide: Boolean): Boolean {
        if (hidden == hide) return false
        hidden = hide
        return true
    }
}

internal object ThemeAccent {
    fun notificationColor(context: Context): Int = Color.rgb(84, 140, 255)
}

internal object InjectGuard {
    private val injecting = AtomicBoolean(false)
    fun active(): Boolean = injecting.get()
    fun set(on: Boolean) { injecting.set(on) }
}

/** Inert trampoline target: the HUD has no settings page to link to. */
class LinkTrampolineActivity : Activity() {
    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)
        finish()
    }
}
