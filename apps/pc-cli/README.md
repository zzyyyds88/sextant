# sextant（电脑侧命令行客户端）

> Sextant 项目的电脑侧 CLI；可执行文件仍为 `sextant`。

用电脑驱动手机上的守护进程。第一次用 `sextant pair` 经 adb 完成配对，之后每条命令都是
一条带证书 pin 的 HTTPS 请求。

设计真源：`docs/DESIGN.md` §9.5。设备端见 `modules/sextant-core/README.md`。

## 构建

```bash
apps/pc-cli/build.sh
# 产物：dist/sextant-linux-amd64、dist/sextant-windows-amd64.exe（都是静态二进制）
```

## 配对（只需要一次）

```bash
# adb 必须可用，且本项目的 adb key 在 adbhome/
export SEXTANT_ADB_HOME="$PWD/adbhome"   # 你自己的 adb HOME；仓库不含 adb key
sextant pair --serial <adb-serial>
```

`pair` 做四件事：

1. 请求 `GET /v1/health` 确认守护可达，并记下服务端证书指纹；
2. 经 `adb shell su -c 'sextant-core ctl pair new --agent pc'` 在设备上取一次性码
   （码只走 adb，不过网络）；
3. `POST /v1/pair` 用该码换长期 token；
4. 把 `server` / `token` / 证书指纹写进 `~/.sextant/config.json`（0600）。

代码一次性、5 分钟过期、错误 3 次锁定。

没有内置默认 serial；用 `--serial` 或环境变量 `SEXTANT_SERIAL` 指定。

## 一条命令初始化

```bash
printf '%s\n' '<解锁图案>' | sextant init --serial <adb-serial>
# 需要 SIM PIN 时：第二行是 PIN（4-8 位数字，可空）
printf '%s\n%s\n' '<解锁图案>' '<SIM PIN>' | sextant init --sim
```

`init` 依次做：检查 health → 未配对时自动 `pair` → 从 stdin 读解锁图案并写入设备
（`ctl secret set-unlock`，值不进 argv/日志）→ 可选 SIM PIN → 默认关闭窗口动画并把原值
记录到设备 `state/baseline/animation.json` → 打印 status。

- 保留动画：`--keep-animation`
- 还原动画：`sextant init --restore`

## 命令

`sextant help` 打印完整清单（表驱动，与分发同源）。按用途分组：

| 组 | 命令 |
|---|---|
| 配对 | `pair [--serial S] [--agent NAME]`、`init [--sim] [--keep-animation] [--restore]` |
| 读 | `health`、`status`、`device`、`lock-state`、`watchdog`、`metrics`、`doctor`、`log [--lines N]`、`doc [-o file]` |
| 锁与屏 | `wake`、`keep-awake on\|off [--timeout-ms N]`、`lock`、`unlock`、`sim-unlock`、`rotation [值]`、`screen --brightness N --timeout-ms N`、`airplane on\|off`、`wifi on\|off` |
| 输入 | `tap X Y`、`swipe X1 Y1 X2 Y2 [ms]`、`longpress X Y [ms]`、`key KEYCODE`、`global back\|home\|…`、`text TEXT [--ime] [--clear]` |
| 感知 | `frame [-o frame.jpg] [--cached]`、`observe [--tree] [--ocr] [--pixels] [--frame]`、`page`、`diff --before F [--after F]`、`tree [--summary] [--fields LIST] [--max-items N] [--viewport]`、`find TEXT [--click]` 或 `find --id RID`、`ocr [--region L T R B] [--fresh]`、`panel`、`pixels`、`color R G B`、`shot [-o screenshot.jpg]` |
| 通用原语 | `wait TEXT\|--id RID [--absent]`、`wait-stable [--timeout-ms N] [--stable-ms N]`、`act --text T\|--id R\|--node N [--action click\|select\|longpress\|set_text] [--expect-selected] [--precheck-selected] [--allow-danger] [--verify-ms N]`、`list --text-contains T\|--rid R\|--class C [--max N] [--scroll N]`、`options [--group TITLE]`、`readback TEXT\|--rid RID [--scope tree\|ocr]`、`chain '<steps-json>'`、`wait-result ...` |
| 会话 | `sessions`、`claim [AGENT] [--display N]`、`heartbeat`、`release ID` |
| 应用 | `apps`、`foreground`、`launch PKG\|COMPONENT`、`stop PKG`、`open URI` |
| 文件 | `ls PATH`、`get PATH -o FILE`、`put LOCAL [NAME]` |
| 系统 | `setting NS KEY [VALUE]`、`prop NAME`、`notifications`（含 SSE `/v1/notifications/events`）、`sms [--limit N]`、`location` |
| 显示与串流 | `displays`、`display-create [W H DPI]`、`display-rm ID`、`stream [--display N] [--seconds N]` |
| 触发器 | `triggers`、`runs`、`trigger-add NAME TYPE [--pattern P] [--text T] [--interval N]`、`trigger-rm ID` |
| 验收 | `acceptance [--out DIR]` |
| 维护 | `run recipe.json [--resume DIR]`、`skill [-o FILE] [--zip] [--install DIR]`、`token show\|forget`、`version` |

`claim` 会把本次会话写到 `~/.sextant/session.json`（0600），之后所有命令自动带
`X-Sextant-Session`；`release` 删除。`heartbeat` 用本地会话续期，适合长时间思考但
暂时不操作设备的任务。心跳只保活；业务操作才刷新“任务活动时间”，5 分钟无业务操作
会被守护回收并锁屏（D37）。

`--json '{"k":"v"}'` 可以给任何 POST 命令传原始请求体。

### sextant skill

手册随守护发布，内容与 API 版本一致；端点是公开只读，未配对也能下载。

```bash
sextant skill                                  # 打印 Markdown
sextant skill -o SKILL.md                      # 保存
sextant skill --install ~/.agent/skills/sextant # 写成可安装目录（SKILL.md）
sextant skill --zip -o sextant-skill.zip        # 可安装包
```

### sextant acceptance

在真机上跑能力矩阵，逐项记录证据：

```bash
sextant acceptance
# out/acceptance-<ts>/REPORT.md  +  results.json
```

判定规则：

- 只有真机实测通过才记 **PASS**；
- 跑不到的记 **SKIP** 并写明原因，**不计入通过**；
- 失败带上守护返回的原文，报告里 FAIL 排在最前面；
- 有任何 FAIL 时退出码非 0。

## 在 DSH / 脚本里用

```bash
sextant tap 540 1200
sextant key KEYCODE_BACK
sextant text "你好" --ime          # 微信这类自绘输入框只能走 IME
sextant find "扫一扫" --click      # 树里没有就回落 OCR
sextant shot -o /tmp/now.png
sextant tree | python3 -c 'import json,sys; print(json.load(sys.stdin)["count"])'
sextant acceptance                  # 收工前跑一遍，非 0 退出即有问题
```

命令失败时以非零码退出并把 daemon 的结构化错误原样打印（`{"error":{"code",…}}`），
不会被包装成"成功但没做"。
