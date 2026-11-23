# Chopin Pilot（chopin-pilot，客户端 APK）

> Sextant 项目的客户端 APK；当前应用名与包名仍为 Chopin Pilot / `com.chopin.pilot`。

一个**零 root** 客户端：状态页 + 无障碍 HUD + 输入法。设备端的全部特权工作都由
KernelSU 模块 `chopin-core` 以 root 拉起的守护进程完成（`docs/DESIGN.md` D20/D21）。

它**不做**任何特权动作：

- 不申请 KernelSU root、不执行 `su` 拉起引擎、不拉起任何进程；
- 不写 `enabled_accessibility_services`、不写 allowlist；
- 不解析 CLI 文本 —— 状态只读守护发布的 `app/status.json`。

唯一例外是**写解锁凭据**：值经 stdin 传给 `chopin-core ctl secret set-unlock`
（锁屏下没有可用的远程通道，凭据必须能从手机上录入）。这条路径写进 root-only 文件，
App 从不读回值。

## 构建

```bash
apps/pilot/build.sh
# 产物：apps/pilot/dist/chopin-pilot-<version>.apk
```

构建脚本最后会做一条断言：除 `Shell.java` 外不允许出现 `ProcessBuilder("su"`。
签名密钥在 `apps/pilot/build/keystore/`（忽略，升级必须复用）。这是本地自签名
key；准备发布时用 `CHOPIN_RELEASE=1` 显式指定专用 keystore 与密码文件，步骤见
[`docs/RELEASE.md`](../../docs/RELEASE.md)。

## 安装

模块会安装它（`service.sh` 比对 md5 后 `pm install -r`）。手动安装也可以：

```bash
adb install -r apps/pilot/dist/chopin-pilot-*.apk
```

## 状态页

| 显示 | 来源 |
|---|---|
| 守护运行状态/pid/版本 | `status.json` 的 `daemon` |
| 包名/是否安装 | `app.package` / `app.installed` |
| 无障碍 | `app.a11y.state` + `detail` + `component` |
| 后台放行 | `app.bg.{autostart,bgcontrol,doze,ok}` |
| 权限组 | `app.perms` |
| 会话 | `sessions.active/max` |
| 解锁凭据/锁屏 | `unlock.{credential,screen_locked,last_attempt}` |

状态文件的真实位置是 `/data/user/0/com.chopin.pilot/files/status.json`（守护写入该 App
私有目录的副本）；`/data/adb` 是 0700 root，App 连目录都进不去，所以真源在那里、
可读副本在这里，两份内容一致。

schema 不匹配时页面显式报错，不猜。

## 组件

| 组件 | 作用 |
|---|---|
| `MainActivity` | 状态页、解锁按钮、凭据录入（showWhenLocked，锁屏可开） |
| `hud/RemoteA11yService` | 无障碍 HUD：状态胶囊 + 停止；`TYPE_ACCESSIBILITY_OVERLAY` |
| `ime/RemoteImeService` | 输入法服务（`com.chopin.pilot/.ime.RemoteImeService`） |
| `ime/ImeControlProvider` | 控制 provider（authority `com.chopin.pilot.ime`，与包名和 service 不同） |

IME 的三个字符串必须分开配置，混用会让文字注入永远失败（已发生过的 bug）。

## HUD 与守护的关系

HUD 的显示状态由守护持有（`internal/hud`），APK 只是渲染：

- `POST /v1/hud/heartbeat` 报告活着并取回要显示的内容；
- `POST /v1/hud/pause` 暂停——**守护会拒绝新的写会话**，因为人在看屏时不该被挪动；
- `POST /v1/hud/stop` 立即释放全部会话；
- `GET /v1/hud/events` 是 SSE，首帧即当前状态。

这些端点只在 loopback 可用（app token 本身是 LocalOnly），远端调用返回 403。
