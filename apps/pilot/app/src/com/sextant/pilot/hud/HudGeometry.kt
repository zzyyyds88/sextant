// Screen geometry shared by the control's ball and capsule. Derived from the reference
// project's pet/EdgeSnap.kt (MIT); only the primitives the HUD uses are kept.

package com.sextant.pilot.hud

import kotlin.math.abs
import kotlin.math.roundToInt

/** The screen and what to keep clear of it (status / navigation bars, the camera cutout), in px. */
internal data class SafeArea(
    val screenW: Int,
    val screenH: Int,
    val left: Int,
    val top: Int,
    val right: Int,
    val bottom: Int,
)

internal enum class Side { LEFT, RIGHT }

/** Where the ball rests: an edge, and how far down the usable height (0 top … 1 bottom). */
internal data class Dock(val side: Side, val yFraction: Float) {
    companion object {
        val DEFAULT = Dock(Side.RIGHT, 0.62f)
    }
}

internal data class WindowPos(val x: Int, val y: Int)

/** Dragging and the snap to the nearest side. */
internal object EdgeSnap {
    const val FLING_PX_PER_S = 1200f

    fun minX(area: SafeArea, margin: Int) = area.left + margin
    fun maxX(area: SafeArea, w: Int, margin: Int) = maxOf(minX(area, margin), area.screenW - area.right - margin - w)
    fun minY(area: SafeArea, margin: Int) = area.top + margin
    fun maxY(area: SafeArea, h: Int, margin: Int) = maxOf(minY(area, margin), area.screenH - area.bottom - margin - h)

    fun sideFor(x: Int, w: Int, area: SafeArea, velocityX: Float): Side {
        if (abs(velocityX) >= FLING_PX_PER_S) return if (velocityX < 0) Side.LEFT else Side.RIGHT
        val center = x + w / 2f
        return if (center < area.screenW / 2f) Side.LEFT else Side.RIGHT
    }

    fun dockFor(x: Int, y: Int, w: Int, h: Int, area: SafeArea, margin: Int, velocityX: Float = 0f): Dock {
        val side = sideFor(x, w, area, velocityX)
        val lo = minY(area, margin)
        val hi = maxY(area, h, margin)
        val f = if (hi <= lo) 0f else ((y - lo).toFloat() / (hi - lo)).coerceIn(0f, 1f)
        return Dock(side, f)
    }

    fun position(dock: Dock, w: Int, h: Int, area: SafeArea, margin: Int): WindowPos {
        val x = if (dock.side == Side.LEFT) minX(area, margin) else maxX(area, w, margin)
        val lo = minY(area, margin)
        val hi = maxY(area, h, margin)
        val y = lo + ((hi - lo) * dock.yFraction.coerceIn(0f, 1f)).roundToInt()
        return WindowPos(x, y)
    }

    fun clampDrag(x: Int, y: Int, w: Int, h: Int, area: SafeArea): WindowPos = WindowPos(
        x.coerceIn(0, maxOf(0, area.screenW - w)),
        y.coerceIn(area.top, maxOf(area.top, area.screenH - area.bottom - h)),
    )
}
