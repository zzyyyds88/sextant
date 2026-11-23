package com.google.android.accessibility.selecttospeak

import android.accessibilityservice.AccessibilityService
import android.view.accessibility.AccessibilityEvent

/**
 * 微信放行兼容服务：零功能，只为让一个「微信白名单认得的组件名」出现在
 * enabled_accessibility_services 里，并且真的被系统绑定。
 *
 * 实机 A/B（本机，同一时刻，唯一变量 = 这个服务在不在）：
 *   只有 .hud.RemoteA11yService            → 微信 1 节点 0 文字
 *   + 本服务（真绑定）                      → 微信 420 节点 81 文字，聊天列表/时间戳/草稿全可读
 *   只把组件串写进 settings（类不存在，绑不上）→ 仍 1 节点
 * 所以微信要的是「真的绑上了这么个名字」，不是字符串本身。
 *
 * 零功能是硬约束：不读节点、不读事件内容、不下发手势、不写输入框。真正干活的仍然是
 * .hud.RemoteA11yService，它不伪装。这样「伪装」和「功能」不落在同一个服务上。
 */
class SelectToSpeakService : AccessibilityService() {

    override fun onAccessibilityEvent(event: AccessibilityEvent?) {
        // 有意为空：本服务不读任何内容。
    }

    override fun onInterrupt() {
        // 零功能：无反馈通道。
    }
}
