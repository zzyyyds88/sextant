window.SEXTANT_I18N = {
  "zh-CN": {
    title: "Sextant Pilot 远控",
    loginHint: "粘贴 API token 或 onboarding JSON。token 只保存在本标签页的 sessionStorage，不写入 localStorage。",
    tokenLabel: "API token",
    tokenPlaceholder: "cpv1_... 或 {\"api_token\":\"...\"}",
    login: "连接",
    logout: "退出",
    connecting: "连接中…",
    connected: "已连接",
    disconnected: "已断开",
    readOnly: "只读",
    active: "控制中",
    free: "空闲",
    claim: "接管控制",
    release: "释放控制",
    noSession: "当前没有写会话；接管后可输入。",
    holder: "当前控制者",
    you: "你",
    display: "屏幕",
    main: "主屏",
    virtual: "虚拟屏",
    webrtc: "实时流",
    snapshot: "快照",
    snapshotBanner: "实时流不可用，已切到快照模式（约 2fps）。",
    stageHint: "接管控制后可点击/滑动",
    textPlaceholder: "输入要发送到手机的文本",
    sendText: "发送文本",
    clearText: "清空",
    back: "返回",
    home: "桌面",
    enter: "回车",
    delete: "删除",
    tokenInvalid: "token 无效或已吊销",
    sessionConflict: "屏幕已被占用，当前为只读。",
    hudUnavailable: "HUD 不可用，无法建立写会话。",
    claimFail: "接管失败",
    inputDisabled: "只读模式下不能输入。",
    sendFail: "发送失败",
    viewerLimit: "实时流 viewer 已满，使用快照模式。",
    unknown: "未知错误",
    heartbeatLost: "会话已失效，请重新接管。",
    retry: "重试实时流",
    version: "版本",
    accessLabel: "在别的设备上打开",
    copyUrl: "复制地址",
    copied: "已复制",
    railDaemon: "守护",
    railA11y: "无障碍",
    railBg: "后台",
    railSessions: "会话",
    railUptime: "运行",
    activityIdle: "没有动作",
    activityLive: "正在操作",
    activityPaused: "已暂停",
    activityStopped: "已停止"
  }
};
window.sextantLang = "zh-CN";
window.t = function (key) {
  var table = window.SEXTANT_I18N[window.sextantLang] || window.SEXTANT_I18N["zh-CN"];
  return table[key] || key;
};
window.applyI18n = function (root) {
  root = root || document;
  root.querySelectorAll("[data-i18n]").forEach(function (el) {
    el.textContent = window.t(el.getAttribute("data-i18n"));
  });
  root.querySelectorAll("[data-i18n-placeholder]").forEach(function (el) {
    el.setAttribute("placeholder", window.t(el.getAttribute("data-i18n-placeholder")));
  });
  document.title = window.t("title");
};
