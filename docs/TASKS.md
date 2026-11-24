# Sextant 任务板（TASKS）

> 状态：**远控主线已收敛，3.0 后展示名改为 Sextant**（2026-11-23）
> 设计真源：`docs/DESIGN.md`（D6–D39）
> 工作主机：本机 `/root/chopin-ksu`；外部研究资料 `/root/chopin-research/`
> 设备：`adb -s <adb-serial>`（<device-model> / MIUI V14 / Android 13 / KernelSU 3.1.8）
> 名称说明：2026-11-18 3.0 之前的记录使用旧名 `chopin-harness` / `chopin-remote` / `com.chopin.remote`；3.0 使用 `chopin-core` / `chopin-pilot` / `com.chopin.pilot`；D38 后当前为 `sextant-core` / `Sextant Pilot` / `com.sextant.pilot`。
> 项目名：**Sextant**；D38 全量技术改名（CLI `sextant`、模块 `sextant-core`、APK `com.sextant.pilot`）；本表历史记录保留旧名。

## 0. 当前结论

- 工作区只保留远控主线：`modules/sextant-core`、`apps/pilot`、`apps/pc-cli`。
- D38 改名：模块 id/显示名 `sextant-core` / `Sextant Core`，软件包名/显示名 `com.sextant.pilot` / `Sextant Pilot`，CLI 为 `sextant`；旧 `chopin-*` 身份迁移后删除。
- `chopin-compat`、`miui14-agent-tune` 已移出工作区，并已从设备卸载；远控守护不依赖它们。
- ROM、反编译、移植、旧构建、外部参考项目已迁到 `/root/chopin-research/`，不纳入 Git，不作为设计真源。
- 旧设计归档已删除（历史由 Git 记录）。
- 实施方式：按 S0–S15 纵切，**每切片以真机验收为准**。

## 状态图例

| 标记 | 含义 |
|---|---|
| `DONE` | 已完成并有验收证据（真机或指定命令输出） |
| `DOING` | 正在执行 |
| `BLOCKED` | 有明确阻塞，需用户决策或外部条件 |
| `TODO` | 未开始 |
| `REMOVED` | 已按用户决策移除（保留历史证据，不保留实现） |

## 切片状态

| ID | 任务 | 状态 | 验收/证据 |
|---|---|---|---|
| S0 | 设计收口、工作区收敛 | `DONE` | 本文件 + `DESIGN.md` + `AGENTS.md`；静态门禁 27 项 0 失败 |
| S1 | 制品与宿主：模块 zip、daemon/ctl 同 ELF、TLS/token/配对/status、APK 去 root | `DONE` | 刷 zip → 重启 → 守护 uid 0 `u:r:su:s0`；`chopin pair` → `health` → `status`；APK 状态页一致 |
| S2 | 解锁 + 输入 + 截图 | `DONE` | 熄屏锁屏 → 自动解锁；tap 命中；中文 text 注入；截图 1080x2400 非全黑 |
| S3 | UI 感知与复合原语 | `DONE` | tree 84+ 节点；OCR 23–44 行带 bounds；find 树优先、无则 OCR；wait/chain/events 真机通过；微信兼容服务绑定后可读 421 节点 |
| S4 | 会话/并行/虚拟屏/流 | `DONE` | 主屏+虚拟屏并行；第三人 409；虚拟屏创建/销毁；WebRTC answer 真 SDP；6 秒 300+ RTP 包 |
| S5 | 应用/设置/文件/信息 | `DONE` | apps/launch/stop、settings 读回、files 上传下载字节一致、通知 SSE、短信 SQLite、定位可用性如实上报 |
| S6 | 触发器/WebUI/HUD/IME | `DONE` | 三类触发器真机触发并留 run；WebUI 真机连续出帧；HUD pause/stop 真拦截；IME 中文 VERIFIED |
| S7 | 保活收口 | `DONE` | 清空 a11y 5s 内恢复；force-stop 后 15s 重绑定；UiAutomation 期间仍 bound；重启自动就绪 |
| S8 | PC CLI + 收尾 | `DONE` | `chopin acceptance` PASS 30 / FAIL 0 / SKIP 0；`chopin journey` PASS 20 / FAIL 0；静态门禁收敛 |
| S9 | 通话状态/挂断/录音自检 | `REMOVED`（2026-11-22，D34） | 历史：真拨真挂；`privileged=true`；`record_ready=true`；缺权限时如实上报原因 |
| S10 | 日常设置走系统服务并读回 | `DONE` | 12 项读到真实值；飞行模式切断 Wi-Fi ADB 的取舍已记录 |
| S11 | WebUI/手机页重设计 | `DONE` | 五格 rail + 活动行；浏览器与手机页截图；对比度最低 6.28:1；触摸目标 ≥36px |
| S12 | 音频（麦克风/扬声器/采集/播放） | `DONE` | 静音前后 RMS 对比；路由读回；每种采集源区分“没权限/没声音”；helper 主线程 + 播放超时按负载 |
| S13 | APK 图标、入口地址、布局修复 | `DONE` | 图标五密度 + 真机桌面；`https://<device-host>:8443/ui/` OCR 可读；整页 42 节点全部可见 |
| S14 | 通话上行注入 | `REMOVED`（2026-11-22，D34） | 历史：`incall_music_uplink` 48k 立体声；`getRoutedDevice()` 回报；`speak --incall` 前后 speakerphone 不变 |
| S15 | 全量改名 Chopin Core / Chopin Pilot | `DONE` | 旧模块/旧包已移除；新包 `com.chopin.pilot` privileged + data 副本；a11y/compat bound；冷启动 acceptance 30/0/0；journey 20/0 |
| S16 | 低延迟观测（D28）：helper 快帧/JPEG、frame 字节、observe、帧复用/缓存、HUD ACK、wait 事件 | `DONE` | acceptance 30/0/0；journey 20/0；实测 （私有验收证据） |
| S17 | 初始化与 skill：`chopin init`、`ctl secret set-sim`、动画基线、`/doc/skill`、`chopin skill` | `DONE` | set-sim/clear-sim 真机读回；animation off/restore 还原 1.0；`/doc/skill` 200；CLI 下载/安装成功 |
| S18 | D29 P0 通用原语：observe/act/list/options/readback/page/wait_stable、events 修复、CLI 一致性、`chopin run` | `DONE` | 七个原语真机可用；acceptance 35/0/0；journey 20/0；act 对 SKU 44 返回 verified=false/no_effect；guard 覆盖 tap/swipe/longpress/chain/find；CLI↔端点由 check-contracts 锁定；配方含前置检查与 on_failure=stop/continue |
| S19 | D29 P1：输入安全、keep-awake、断点/进度、上下文裁剪、doctor/log | `DONE` | 输入安全与 keep-awake 真机通过；`run --resume` 失败落盘/跳过成功步骤/成功后清 failed；`tree` 的 `fields`/`max_items`/`summary`/`viewport_only` 生效（5/531）；`doctor` 8 项全 ok；`log` 读同一日志；acceptance 37/0/0、journey 20/0 |
| S20 | D29 P2 / D33：增量树 diff、内存 metrics、页面指纹/版本与地图/配方漂移 | `DONE` | `diff` 同页 0 变化、切屏 added 49/removed 169、过期 404；`metrics` 计数增长且 <2KB；地图漂移正反例；acceptance PASS 41 / FAIL 0 / SKIP 0；journey PASS 21 / FAIL 0 |
| S21 | D30 拟人操作与验证码分流：种子化落点/间隔/限速、challenge 分类与滑块自解、risk/cooldown、handover | `DONE`（检测分流部分 SKIP 说明见债务） | 同 seed 两次 tap 落点一致；10 次 tap interval 702–1482ms（区间 700–1600）、dispatch 36–62ms；handover 后注入 409、return 后恢复；设置页 challenge=none；滑块/短信验证码页无真机样本，分类与分流以单测覆盖（22 项新增） |
| S23 | D32 agent 引导入口：`/doc` 的 `x-chopin-guidance`（install/use）与 SKILL.md「从零安装」 | `DONE` | `GET /doc` 返回 10 步、两阶段；`/doc/skill` 含「从零安装」；init 命令只经 stdin；单测锁定字段与阶段 |
| S27 | D38 Sextant 全量技术改名：CLI/模块/APK/状态目录/请求头/环境变量 + 迁移脚本 | `DONE` | 静态门禁 28/0；真机迁移完成（旧模块/APK/守护消失）；doctor 8/8；acceptance PASS 41 / FAIL 0 / SKIP 0；journey PASS 21 / FAIL 0 |
| S28 | D39 `/doc` 人机双入口：HTML 说明页 + `/doc/openapi.json`/`/openapi.json`，agent 本地缓存 skill/token | `DONE` | 真机 `GET /doc` 返回 HTML（15 分组、120 端点行、无外部资源），`/doc/openapi.json` 与 `/openapi.json` 均为 OpenAPI 3.1（113 paths），`Accept: application/json` 仍返回 JSON；HTML 页头 CSP/nosniff/no-store；`/doc/skill` 标为公开；单测覆盖协商、别名、分组全覆盖；静态门禁 28/0；doctor 8/8；acceptance PASS 40 / FAIL 0 / SKIP 1（find 无探测串）；journey PASS 21 / FAIL 0。子代理首次接入：按 `/doc` 鉴权、缓存 SKILL.md 与 0600 token、health/status/page 全 200；第二个子代理只读本地缓存完成同样三个请求，`paired_at` 未变、未重新抓 `/doc`、未重新配对；最终构建 2026-11-24 真机复核：acceptance PASS 40 / FAIL 0 / SKIP 1（find 无探测串）、journey PASS 21 / FAIL 0、doctor 8/8，act 示例/错误码/鉴权文案与页面逐项核对通过 |
| S26 | D37 会话活动信号与僵尸回收：业务请求续期、`zombie_sec` 300s、CLI 本地会话 + heartbeat | `DONE` | 真机：只心跳不操作 6s 被回收并锁屏；持续 page 6s 不被回收；release 锁屏；CLI 后续命令自动带 session；acceptance 41/0/0、journey 21/0（首次 map 抽样失败重跑通过） |
| S25 | D36 任务收尾与屏幕联动：`finishTask`、`stale_sec` 180s | `DONE` | 正常 release 后 `task finished (release); screen locked` 且 screen OFF/keyguard true；短 stale 配置下异常回收并锁屏；HUD stop 也收尾锁屏；acceptance 41/0/0、journey 21/0 |
| S24 | D35 会话后台回收：`session.reap_sec` 后台 reaper | `DONE` | 真机 idle_sec=3/reap_sec=1：claim 后 8s 会话消失，审计 `session: reaped 1 expired session(s)`；单测周期/取消/0 禁用；acceptance 41/0/0、journey 21/0 |
| S22 | D31 App 地图层：YAML 地图（PhoneGo 兼容）、文本重叠识别、操作目录、相对宏、回放、爬取、bench、`map_*` 与 `map_nav` | `DONE`（有已知限制） | 设置 19 屏、`complete=true`、1080x2400；`--seed 1 --limit 5` exact 5 / same_app 5；可直接 Load PhoneGo 的 50 屏 YAML 并导出 282 个操作；acceptance 40/0/0、journey 21/0（含 map 步骤）；爬取前后 wifi/蓝牙/飞行状态不变 |

## 真机验收快照

| 时间 | 内容 | 结果 | 证据 |
|---|---|---|---|
| 2026-11-16 | 基线：守护 root、配对、health/status、锁屏解锁、截图、tree | 通过 | （私有验收证据） |
| 2026-11-17 | 扩展能力矩阵 30 项 | PASS 30 / FAIL 0 / SKIP 0 | （私有验收证据） |
| 2026-11-17 | 真实使用流程 20 步（含 HUD 5 场景） | PASS 20 / FAIL 0 | （私有验收证据） |
| 2026-11-17 | HUD 实物与截图隐藏对比 | 通过 | （私有验收证据） |
| 2026-11-17 | WebUI/手机页视觉验收 | 通过 | （私有验收证据） |
| 2026-11-18 01:28 | **工作区收敛后重新配对 + 能力矩阵** | **PASS 30 / FAIL 0 / SKIP 0** | （私有验收证据） |
| 2026-11-18 02:29 | **移除旧模块后完整能力矩阵** | **PASS 30 / FAIL 0 / SKIP 0** | （私有验收证据） |
| 2026-11-18 03:50 | **3.0 Core/Pilot 迁移后 journey** | **PASS 20 / FAIL 0** | （私有验收证据） |
| 2026-11-18 03:55 | **3.0 冷启动后完整能力矩阵** | **PASS 30 / FAIL 0 / SKIP 0** | （私有验收证据） |
| 2026-11-18 05:18 | 音频日志修复后热部署：能力矩阵 + journey | **PASS 30 / FAIL 0 / SKIP 0；journey PASS 20 / FAIL 0** | `acceptance`（私有验收证据） / `journey`（私有验收证据） |
| 2026-11-18 05:18 | 音频播放日志实测：`audio: played 6400 bytes via speaker=true`（此前为 `%!s(float64=…)`） | 通过 | 本轮日志新增行；`audio say` 播放 0.2s 静音 PCM |
| 2026-11-18 06:30 | `/doc` 修复后热部署：能力矩阵 | **PASS 30 / FAIL 0 / SKIP 0**；96 paths；`servers` 用请求 Host；`health`/`pair`/`doc` 显式 `security: []` | `acceptance`（私有验收证据） |
| 2026-11-18 07:24 | **D28 热部署后能力矩阵** | **PASS 30 / FAIL 0 / SKIP 0**；100 paths | `acceptance-2026-11-18-d28.md`（私有验收证据） |
| 2026-11-18 07:25 | **D28 使用流程** | **PASS 20 / FAIL 0** | `journey-2026-11-18-d28.md`（私有验收证据） |
| 2026-11-18 07:30 | D28 延迟实测（20 次采样） | frame/screenshot/pixel/OCR 达标；tap、observe 未达目标，数据与原因见证据 | `perf-2026-11-18-d28.md`（私有验收证据） |
| 2026-11-18 09:47 | **D29 P0 扩展能力矩阵（含 page/list/options/wait_stable）** | **PASS 34 / FAIL 0 / SKIP 0** | `acceptance-2026-11-18-d29.md`（私有验收证据） |
| 2026-11-18 09:48 | **D29 P0 使用流程回归** | **PASS 20 / FAIL 0** | `journey-2026-11-18-d29.md`（私有验收证据） |
| 2026-11-18 09:52 | **D29 P0 收尾：guard 全覆盖 + CLI 覆盖门禁 + act 错误路径** | **PASS 35 / FAIL 0 / SKIP 0；journey PASS 20 / FAIL 0** | `acceptance`（私有验收证据） / `journey`（私有验收证据） |
| 2026-11-18 10:54 | **D29 P1：输入安全 + keep-awake** | **PASS 36 / FAIL 0 / SKIP 0；journey PASS 20 / FAIL 0**；set_text 写入/清空 verified=true；precheck 409；keep-awake held/released | `acceptance`（私有验收证据） / `journey`（私有验收证据） |
| 2026-11-18 11:04 | **D29 P1：上下文裁剪 + doctor/log** | **PASS 37 / FAIL 0 / SKIP 0**（新增 tree-crop）；doctor 8 项全 ok；`log` 读到守护日志 | `acceptance`（私有验收证据） / `journey`（私有验收证据） / `P1 证据`（私有验收证据） |
| 2026-11-18 11:07 | **D29 P1：run 断点/resume** | 失败落盘 `done/p1 failed/p2`；resume 跳过 p1、重跑 p2、`failed` 清空 | `P1 证据`（私有验收证据） |
| 2026-11-19 05:45 | **D30 热部署后真机验证（拟人/验证码/handover）** | 同 seed 落点一致 (335,1952)；10 次 tap interval 702–1482ms、dispatch 36–62ms；handover 409 拦截与 return 恢复；challenge=none、risk 为空 | 本轮命令输出（见提交说明）；滑块/验证码页 SKIP |
| 2026-11-19 08:35 | **D31 设置地图（YAML）：build 8 屏 + seed=1 bench + nav/配方** | 1080x2400；exact 2/3、same_app 2/3；PhoneGo 地图兼容（50 屏/282 ops）；`map nav` 到 WLAN 成功；`map_nav` 配方成功；无线电前后一致 | 本轮命令输出；地图在 `/root/.chopin/maps/com.android.settings.yaml` |
| 2026-11-19 08:53 | **D31 15 屏地图 + D30/D31 进入验收矩阵** | 15 屏；seed=1 limit=5 exact 5/5；`acceptance` PASS 40 / FAIL 0 / SKIP 0；`journey` PASS 21 / FAIL 0 | `out/acceptance-20261119-085335/REPORT.md`、`out/journey-20261119-085426/REPORT.md` |
| 2026-11-19 09:10 | **D31 地图跑到队列耗尽** | 19 屏、complete=true；seed=1 limit=5 exact 5/5、same_app 5/5 | `/root/.chopin/maps/com.android.settings.yaml`；本轮 bench 输出 |
| 2026-11-19 09:30 | **D32 引导入口** | `/doc` 10 步（install 5 + use 5）；`/doc/skill` 从零安装；真机 curl 可读 | 本轮 curl 输出；提交 `D32` |
| 2026-11-21 16:13 | **S20/D33 深度能力（剪贴板之外的三项）** | metrics 22 请求/多路由、响应 1.3KB；diff 同页零变化、切屏 added 49/removed 169、过期 404；地图漂移 live/drift 两例 | `out/spike-*.json` |
| 2026-11-23 09:00 | **Sextant 4.0.0 公开发布** | GitHub Release `v4.0.0`，tag 指向 4.0.0 源码；资产 APK/模块/CLI/校验和/GPG/LICENSE/NOTICE 齐全 | `https://github.com/zzyyyds88/sextant/releases/tag/v4.0.0` |
| 2026-11-23 08:23 | **D38 Sextant 全量改名真机迁移** | 旧 `chopin-core` / `com.chopin.pilot` / 旧守护均消失；新包 `com.sextant.pilot` + a11y/后台正常；doctor 8/8；acceptance 41/0/0；journey 21/0 | 迁移备份保留在私有环境 |
| 2026-11-22 15:40 | **D37 会话活动与僵尸回收回归** | 僵尸回收+锁屏、持续业务操作免回收、release 锁屏均通过；acceptance PASS 41 / FAIL 0 / SKIP 0；journey PASS 21 / FAIL 0（首次 map 抽样 landed screen_0，重跑通过） | `acceptance`（私有验收证据） / `journey`（私有验收证据） |
| 2026-11-22 15:10 | **D36 任务收尾与屏幕联动回归** | release 收尾锁屏、异常回收锁屏、HUD stop 收尾均写审计；acceptance PASS 41 / FAIL 0 / SKIP 0；journey PASS 21 / FAIL 0 | `acceptance`（私有验收证据） / `journey`（私有验收证据） |
| 2026-11-22 14:53 | **D35 会话后台回收回归** | acceptance PASS 41 / FAIL 0 / SKIP 0；journey PASS 21 / FAIL 0；短超时真机会话被后台回收并写审计 | `acceptance`（私有验收证据） / `journey`（私有验收证据） |
| 2026-11-22 12:39 | **D34 通话能力移除后的回归** | acceptance PASS 41 / FAIL 0 / SKIP 0；journey PASS 21 / FAIL 0；设备包记录 0 处通话权限/组件；`audio speak` rate=16000 `routed_to=speaker` | `acceptance`（私有验收证据） / `journey`（私有验收证据） |
| 2026-11-21 16:53 | **S20 回归（剪贴板移除后）** | acceptance PASS 41 / FAIL 0 / SKIP 0；journey PASS 21 / FAIL 0（含地图建图/导航） | `acceptance`（私有验收证据） / `journey`（私有验收证据） |
| 2026-11-17 22:27 | 真拨真挂 + 通话中上下行录音 + 静音只静上行 | 通过 | `docs/TASKS.md` 历史记录 / Git 历史 |
| 2026-11-18 00:17 | 上行注入按 vendor 端口 48k 立体声开 track；incall 不碰扬声器 | 通过 | 最近提交 `5f49c9c` |
| 2026-11-18 01:21 | 工作区收敛：旧模块/研究资料外迁、文档重构、静态门禁 | 静态 27 项 0 失败；设备守护在线 | 本轮提交 |

## 工作区收敛记录

- **移出工作区**：`modules/chopin-compat`、`modules/miui14-agent-tune` 及其 zip；`docs/archive/2026-11-pre-rewrite`；`decompiled/`、`research/`、`audit/`、`downloads/`、`extract/`、`old/`、`.uploads/` 及对应反编译脚本/工具。
- **外部研究资料**：`/root/chopin-research/`（decompiled、downloads、extract、old、research、audit、tools、uploads）。
- **保留**：`research/dsh-go`、`research/scrcpy*`、`research/miui-a11y-keepalive` 等直接相关参考，但已随 `research/` 移到外部目录；本工作区不再包含。
- **设备侧**：已卸载 `chopin-compat` / `miui14-agent-tune`（watchdog 停止、Portal a11y/deviceidle 清除、SecurityCenter 组件恢复）；旧 `chopin-harness` 模块与旧包 `com.chopin.remote` 已移除；新 `chopin-core` / `com.chopin.pilot` 已安装，包为 `PRIVILEGED` + `UPDATED_SYSTEM_APP`，a11y/compat 均 bound。Uperf 是独立模块，仍保留。

## 3.0 迁移记录（2026-11-18）

- 迁移顺序：停旧守护 → 装 `chopin-core`（`customize.sh` 从旧目录复制 `unlock-secret` / `tokens.json` / `pki`）→ 卸 `chopin-harness`（停旧守护、删旧包、清旧 a11y、删旧数据）→ 重启一次 → 冷启动验收。
- 旧状态：`tokens.json` + `pki` 复制成功，PC 旧 token 仍可用；旧包 `com.chopin.remote` 已 `pm uninstall`；旧模块目录已消失。
- **ROM 硬约束（新发现）**：只把 APK 放在 `/system/priv-app/ChopinPilot` 时，App 进程读不到该 overlay 路径（`ResourcesManager`/`ziparchive` ENOENT → `ContextImpl` Resources NPE → a11y 永不绑定）。必须同时保留 `/data/app` 更新副本；`service.sh` 检测到只有 priv-app 路径时会 `pm install -r` 创建副本，包仍为 `PRIVILEGED` + `UPDATED_SYSTEM_APP`。
- **CE 硬约束**：守护在首次解锁前启动，`/data/user/0/com.chopin.pilot` 不可用；`publishAppFiles` 现在在 keepalive 里重试，解锁后发布 `status.json` / `api.json` / `app-token` / `hud.json`。否则 HUD 永远不 provision，journey 的 HUD 步骤会失败。
- **scope 修正**：`GET /v1/hud` 接受 `read` **或** `hud`；普通 PC token 与 HUD 自己的 token 都能读同一状态（都受 loopback 限制），写通道仍只接受 `hud`。此前只接受 `read` 让 journey 用 HUD token 读回 403，表现为“叠加层不可见/暂停被心跳清掉”的假故障。
- **不保留回退**：迁移完成后已删除旧模块 zip/APK 备份、设备 `/data/local/tmp` 旧产物，以及 `customize.sh`/`service.sh` 中从 `chopin-harness` / `chopin-remote` 复制状态的迁移分支。当前只支持 `chopin-core` 自身升级。

## 已知未覆盖 / 债务

- HUD 视觉、WebRTC 观感只能人眼看；验收矩阵只验证接口/窗口/帧数/字节数。
- 短信发送只验证请求被接受与状态可查，不真的往陌生号码发短信。
- 解锁图案“忘记图案”分支无法安全演练。
- 微信白名单依赖其当前版本的组件名，升级后需复验。
- 多用户/分身只测了 user 0。
- D28 目标未完全达到：`tap` P95 248ms（目标 ≤150）、`observe` P95 1028ms（目标 ≤700）。
  剩余成本与后续方向见 （私有验收证据）；不把未达标写成达标。
- D29 P1 的请求级统一 trace 尚未落地：`observe` 已有 `timings`，但每步的 `frame_id`/`verified`/`timings` 还没写进 run state；P2 尚未开始。
  结算安全暂不处理（用户决定）；act 的成功路径仍由 CLI 真机验证，acceptance 只覆盖错误路径。
- 危险按钮识别是文本匹配（含“立即购买/去结算/提交订单”等），WebView 自绘无文本按钮覆盖不到；
  不把它当保险箱，坐标 tap 仍可绕过。
- **D30 SKIP**：本轮设备上没有滑块/短信验证码页样本，`challenge` 的正向分类、`solve` 的拖动与
  `challenge_needs_human` 只在单测夹具上验证；真机只验证了 `none` 路径、handover 409 与限速。
  下次遇到验证页时补一次真机记录，不把单测当 PASS。
- **D30 代价**：拟人限速使连续动作端到端间隔被拉到 700–1600ms，`acceptance`/`journey` 总耗时上升；
  D28 的 tap 指标改看 `dispatch_ms`（本轮 36–62ms），不再用端到端耗时对比。
- **D31 已知限制**：没有 LLM 富化/分类，动态文本（用户名 `<user>`、SSID）会进入操作目录；
  账号副标题这类入口可能落到相邻行，产生一条错误边（已计入 bench 失败）；爬取只支持
  display 0；8 屏浅图不代表全量精度。
- **D33 diff 语义**：按前序结构路径配对，节点插入会让后续路径整体平移，changed 数偏大；
  这是明确的“结构差异”，不承诺最小编辑距离，验收按此语义断言。
- **D31/journey 地图抽样波动**：2026-11-22 的 D37 回归里 map 步骤首次
  `landed screen_0 expected screen_1`，同一轮重跑 PASS。属于动态入口抽样，不是
  会话/锁屏回归；记录以免把单次绿/红当成稳定。
- **D35 配置债务**：`session.linger_sec` 在代码里没有使用点；因配置加载使用
  `DisallowUnknownFields`，直接删除会让旧 `core.json` 解析失败，暂保留并记为债务，
  待单独做配置迁移或赋予真实语义。
- **D33 地图漂移**：旧 19 屏地图是滚动并集，首页可视区域的 coverage 可能低于 0.5，
  `map where` 对首页可能识别为空（D31 已记录的覆盖限制）；漂移正反例用真实当前页
  文本与真实指纹构造的最小地图夹具验证，旧地图未重新爬取。

## 下一步最安全入口

```bash
tools/scripts/run-all-static.sh
HOME=$PWD/adbhome adb connect <adb-serial>
sextant pair --serial <adb-serial>
apps/pc-cli/dist/sextant-linux-amd64 acceptance
```

停止条件：任何 FAIL 先复现并保留原文；SKIP 必须写明原因；不得把 SKIP 算成 PASS。

## 后续处理记录（2026-11-18）

1. **设备旧模块（已处理）**：`chopin-compat` 已停止 watchdog/Uperf 管理并恢复基线；`miui14-agent-tune` 已清除 `com.mobilerun.portal` 的 a11y 项与 deviceidle 白名单。两模块目录带 `remove` 标记，下次开机由 KernelSU 清理。移除后完整验收 PASS 30 / FAIL 0 / SKIP 0。
2. **D26 凭据（已决定）**：图案凭据保留为工作区本地文件（0600，gitignore），不提交 Git；`build-module.sh` 在缺失/为空时显式失败并打包。DESIGN D26、AGENTS、WORKSPACE、模块 README 已同步。

