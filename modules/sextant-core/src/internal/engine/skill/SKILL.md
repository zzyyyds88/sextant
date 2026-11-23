---
name: sextant-remote
description: 通过 sextant CLI 操作一台已刷 sextant-core 模块的 Android 手机：初始化、配对、看屏、点击、输入、解锁与审计。
---

# sextant 远程操作手册

## 0. 从零安装

第一次使用时按顺序做；设备已经刷好模块的可以跳到第 1 节。

1. 构建模块 zip：`tools/scripts/build-module.sh modules/sextant-core`，产物是
   `modules/sextant-core.zip`（构建物，不进 Git）。
2. 刷入 KernelSU：

```bash
adb push modules/sextant-core.zip /data/local/tmp/sextant-core.zip
adb shell su -c 'ksud module install /data/local/tmp/sextant-core.zip'
```

   输出以 `Done` 结束。

3. 重启一次（只有启动路径变化时才需要）：

```bash
adb reboot && adb wait-for-device
```

4. 确认守护：`sextant health` 返回 `"ok": true`。
5. 配对与凭据：见第 1 节的 `sextant init`；图案只经 stdin，不进 argv/日志。
6. 自检：`sextant doctor` 逐项 ok 后进入使用。

前置条件：`adb -s <serial> get-state` 返回 `device`；电脑上有 `sextant` CLI；
没有内置默认 serial；用 `--serial` 或 `SEXTANT_SERIAL` 指定。守护监听
`https://<host>:8443`，首次鉴权只走 adb（DESIGN D24）。

`GET /doc` 的 `x-sextant-guidance` 是本节的机器可读版本，命令与期望结果一一对应。

## 1. 一条命令初始化

```bash
printf '%s\n' '<解锁图案>' | sextant init --serial <adb-serial>
```

- 第一行是解锁图案，经 stdin 传给设备，不进入 argv、日志或审计。
- 加 `--sim` 时第二行是 SIM PIN（4-8 位数字，可空）。
- 已配对时不会重复签发 token；未配对时自动完成 `pair`。
- 默认关闭窗口动画并把原值记录到 `state/baseline/animation.json`。
- 撤销动画改动：`sextant init --restore`；保留动画：`--keep-animation`。

## 2. 单独配对

```bash
sextant pair --serial <adb-serial> --agent pc
```

token 与证书指纹保存在 `~/.sextant/config.json`（0600）。之后所有命令直接可用。

## 3. 感知屏幕

```bash
sextant frame -o /tmp/frame.jpg        # 最新帧，image/jpeg，一次往返
sextant observe --tree --frame         # 一次截帧同时返回树与图片
sextant tree                           # 无障碍节点树（JSON）
sextant tree --summary                 # 人类摘要；同时裁掉非决策字段
sextant tree --fields text,clickable --max-items 40 --viewport
sextant ocr                            # ROM OCR 文本与 bounds
sextant find "发送" --click            # 树优先，找不到再用 OCR
```

`observe` 的返回里有 `timings`（`capture_ms`/`tree_ms`/`ocr_ms`），先看哪一段慢。
OCR 默认复用 2 秒内的最近帧；需要重新截帧时传 `fresh=true`（HTTP）或用 `--fresh`。

## 3.1 通用原语（D29）

这些原语覆盖任何 App，不要为单个 App 写专用脚本：

```bash
sextant page                         # 前台/组件/窗口/键盘/页面指纹
sextant diff --before F [--after F]  # 两次 observe --tree 的结构差异
sextant wait "发送" --timeout-ms 10000
sextant wait-stable --stable-ms 300  # 等界面停止变化，替代 sleep
sextant act --text "44" --action select --expect-selected --verify-ms 1500
sextant list --text-contains "¥" --max 20
sextant options --group "鞋码"
sextant readback "任务已完成"         # 发送后回读，不以提示文案判断
sextant keep-awake on                # 长任务期间持 PARTIAL_WAKE_LOCK
sextant metrics                      # 内存请求/UI/helper 计数（重启清零）
sextant run recipe.json              # 线性配方：page/list/options/act/readback/wait_stable
```

`observe --tree` 返回扁平节点（`node_id`/`parent_node_id`/`path`），帧缓存 60 秒；
`diff` 用 `path` 配对，节点插入会让后续路径平移并如实报 changed，不承诺最小编辑。

`act` 的返回必须同时看三个字段：`dispatched`（事件已注入）、`verified`
（预期是否满足）、`failure.code`（`target_not_found`/`target_disabled`/
`offscreen`/`no_effect`/`verify_timeout`/`danger_button`/
`precondition_failed`/`challenge_slider`/`challenge_code`）。`verified=false`
就是没生效，不要当成成功继续。

- `act --action set_text` 默认回读输入框（`expect.input_text`），`verified=true`
  才表示文字真的进去了；`clear` 也一样。
- `act` 默认拒绝结算/支付类按钮（`danger_button`）。`--allow-danger` 只在用户
  明确要求下单时使用；比价/浏览任务永远不要带它。
- 点击前需要“当前状态符合预期”时用 `precheck`（例如只有输入框为空才点发送）；
  不满足返回 `precondition_failed`，不会注入。
- 长任务用 `sextant keep-awake on`，结束必须 `off`；配方可写
  `"keep_awake": true`，`run` 会在结束时释放。

`tree` 默认输出 JSON，`--summary` 才是人类摘要；页面很大时用
`--fields`/`--max-items`/`--viewport` 裁剪，响应里的 `total` 是裁剪前节点数，
`truncated=true` 表示还有没返回的节点。`shot` 默认写 `screenshot.jpg`（实际是
JPEG）。`launch --clear-task` 能回到首页而不是恢复上次页面。

配方失败时 `run` 会打印 `resume: sextant run ... --resume <run目录>`；修好原因后
原命令加 `--resume` 即可跳过已成步骤，只重跑失败步骤及其后续。排查环境用
`sextant doctor`（配对/守护/无障碍/后台/helper/前台逐项判定），看守护日志用
`sextant log --lines 100`。

配方示例（只读，前置检查不满足就停止）：

```json
{
  "name": "inspect-current-page",
  "preconditions": [
    {"id": "foreground", "action": "page", "expect": {"foreground": "com.xunmeng.pinduoduo"}}
  ],
  "steps": [
    {"id": "options", "action": "options", "params": {"group": "鞋码"}},
    {"id": "read", "action": "readback", "params": {"text_contains": "请选择"}}
  ],
  "on_failure": "stop"
}
```

`on_failure: continue` 会跑完剩余步骤但仍以非零退出；默认 `stop`。配方是 JSON，
不是脚本语言：没有变量、循环或分支；条件逻辑留在调用方。

## 3.2 验证码与人工接管（D30）

```bash
sextant challenge                     # 分类当前页：slider / code / none
sextant challenge-solve [--dx N]      # 只处理滑块；短信/邮箱验证码会被拒绝
sextant risk [--package P]            # 哪些 App 有未过期的验证页记录
sextant handover --package com.taobao.taobao --reason "请手动完成验证"
sextant handover --status
sextant handover --return --relaunch  # 用户做完后收回 agent 的屏幕
```

- `challenge` 返回 `kind` 与 `evidence`。词表命中但没有对应控件（搜索结果的
  常见形态）返回 `none`，不要据此停手。
- `kind=code` 表示这是短信/邮箱验证码：**不要代填**，直接 `handover`。
- `kind=slider` 时 `challenge-solve` 会按「拖到最右」尝试；返回
  `challenge_unresolved` 就是没解开，按失败处理，不要循环重试。
- handover 期间所有注入端点返回 409 `handed_over`；用户完成后必须
  `handover --return`，否则 agent 无法继续操作。
- 同一 App 记录达到 3 次时 `sextant run` 会拒绝自动执行；只有用户明确要求才可
  `--force`。

## 3.3 拟人操作与复现（D30）

注入默认带落点随机、动作间隔限速与手势时长缩放。每次注入的响应里有
`point`（实际落点）、`seed`、`paced_ms`。

```bash
sextant run recipe.json --seed 7f3a...   # 同一 seed 重放同一条路径
```

`run` 会自动生成并记录 seed；失败后按提示 `--resume` 会沿用同一 seed。
需要确定性对照（A/B、基准）时把设备配置 `human.enabled` 设为 false。



## 3.4 App 地图（D31）

```bash
sextant map build --package com.android.settings --app Settings \
  --max-screens 15 --max-minutes 15
sextant map list
sextant map where ~/.sextant/maps/com.android.settings.yaml
sextant map ops ~/.sextant/maps/com.android.settings.yaml --nav-only
sextant map nav ~/.sextant/maps/com.android.settings.yaml wlan
sextant map bench ~/.sextant/maps/com.android.settings.yaml --limit 5 --seed 1
```

- 地图存 `~/.sextant/maps/<package>.yaml`（PhoneGo 的 YAML 格式），是电脑侧
  产物，不进守护。
- `build` 会真实点击手机；显式执行该命令即视为同意。爬前记录 WiFi/蓝牙/飞行
  状态，结束恢复；开关、新建、删除、支付类标签默认不点。
- 没有 LLM 富化：`aliases`/`description` 为空，`map ops` 给的是纯文本目录，
  由你（agent）决定用哪个操作。
- `map nav` 先在当前树里按文本找目标，找不到会在有界页数内滚动继续找，
  最后才用录制坐标；当前屏不在目标路径上时回退完整冷启动路径；已经在目标
  屏时返回 0 步。
- 地图屏记录了抓取时的 `fingerprint` 与 `app_version`。识别成功但二者与当前
  页不一致时，`map where/nav/bench` 输出 `drift`/`version_changed` 警告（不中断：
  地图匹配仍有文本重叠证据）；`map_nav` 配方可带 `expect_fingerprint`，不一致
  按步骤失败。警告是“地图可能过期”，不是“识别错误”。

### 怎么用目录选操作

规则照搬 PhoneGo 的 MACRO_PLAN：先判断这件事是「已知路径」还是「新情况」，
不要一上来就逐步试探。

1. `map where FILE` 确认当前屏；不在目标 App 先 `launch`。
2. `map ops FILE --nav-only` 取导航目录，用任务里的词去匹配 op id 与描述
   （中文单字算词，英文单字不算）。
3. 四选一：
   - **直接导航并完成任务** → `map nav FILE OP`。
   - **导航到对，但还要精确动作**（开开关、输入、滚动找内容、发送后回读）
     → `map nav` 到达目标屏，再用 `observe`/`act`/`readback`。
   - **没有匹配操作** → 用通用原语逐步做；不要硬套地图。
   - **只查询**：答案已知就直接回答；答案需要屏幕当前值就用 `readback`
     读取，不要猜。
4. 选择顺序：匹配等级 → 导航优先 → 目标屏不同于当前屏 → id 短者优先；
   同分按目录顺序。
5. `map nav` 返回失败或 `landed != target` 时，不要重试同一个 op：换一个
   候选，或改用通用原语。
6. 支付/充值/下单等仍会被拒绝（`danger_button`）；只有用户明确要求时才
   `--allow-danger`。
7. 用户名、SSID、时间这类动态文本不是稳定入口，别把它当操作名。

配方里可直接用：

```json
{"id": "nav", "action": "map_nav",
 "params": {"map": "/root/.sextant/maps/com.android.settings.yaml", "op": "我的设备"},
 "expect": {"ok": true}}
```

`map bench` 用地图自己的 NAV 操作测量精确落点；失败项照实记录，不要把它当成
“地图已可用”的证明。

## 4. 输入

```bash
sextant tap 540 1200
sextant swipe 540 1900 540 500 300
sextant text "你好"                    # 优先 a11y ACTION_SET_TEXT，不切输入法
sextant text "你好" --ime              # 控件拒绝 a11y 时才走 IME
sextant key KEYCODE_HOME
```

点击优先节点而不是盲坐标；`find --click` 的回复里有 `via: node|ancestor|gesture`。

## 5. 锁屏、会话与安全

```bash
sextant lock-state
sextant unlock                          # 用已保存图案解锁
sextant wake
sextant claim agent-a                   # 写会话；第二个写者会 409
sextant release <session-id>
```

- claim 会先解锁；最后一个写会话结束（release / reap / HUD stop / 断连）会收尾：
  释放 keep-awake、恢复你的输入法、锁屏。异常终止（默认 180s 心跳丢失）也会
  自动回收并锁屏，不需要人工清理。
- CLI 的 claim 会把会话写到 `~/.sextant/session.json`，后续命令自动带
  `X-Sextant-Session`；业务请求会刷新任务活动时间。心跳只保活，不算业务活动；
  长时间思考但不动设备时用 `sextant heartbeat`，否则默认 300s 无业务操作会被
  当作僵尸会话回收并锁屏。
- handover 人工接管期间不锁屏；任务进行中 agent 暂时无动作时不主动息屏。
- HUD 是“有人正在操作”的唯一提示；暂停/停止由手机上的人操作。
- 不要绕过 HUD：所有注入端点都会在暂停/停止时返回 409。

## 6. 应用、文件与设置

```bash
sextant apps
sextant foreground
sextant launch com.tencent.mm
sextant stop com.tencent.mm
sextant put ./local.txt /data/local/tmp/local.txt
sextant get /data/local/tmp/local.txt -o ./copy.txt
sextant setting system screen_off_timeout
sextant settings                        # 日常设置逐项读回
```

## 7. 错误处理

- 所有错误是结构化 JSON：`{"error":{"code","message","detail"}}`。
- 常见 code：`credential_missing`（无解锁凭据）、`text_input_unavailable`
  （没有聚焦输入框）、`session_conflict`（别的写者持锁）、`confirmation_required`
  （危险操作需二次确认）、`unsupported`（该 ROM 不提供该能力）。
- `act` 的动作失败在 200 响应里：`verified=false` + `failure.code`，例如
  `no_effect`（点了但界面没变）、`target_disabled`、`offscreen`。按 code 换目标
  或停止，不要重复点同一个目标超过两次。
- 不做只读兜底、不返回假成功；失败就是失败，按 code 处理。

## 8. HTTP 直接调用

```text
GET  /doc                  OpenAPI 3.1（公开）
GET  /doc/skill            本手册（公开）
POST /v1/ui/observe        {"tree":true,"frame":true}
GET  /v1/ui/frame          image/jpeg 字节
```

除 `/v1/health`、`/doc`、`/doc/skill`、`/doc/skill.zip` 外都要
`Authorization: Bearer <token>`；写操作另加 `X-Sextant-Session`。

## 9. 验收自己

```bash
sextant acceptance       # 端点级 41 项
sextant journey          # 用户流程 21 步，三条判定
```

一个能力只有在“像用户那样操作、换一条通道复核、审计日志确认路径”三条同时成立时才算通过。
