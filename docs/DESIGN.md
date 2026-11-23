# Sextant 设计基线（远控专用）

> 状态：**当前有效（v3 工作区收敛，2026-11-18）**
> Owner：项目用户；执行 agent 只能在确认范围内修改
> **本文件是唯一设计真源。** 任务状态与真机验收记录见 `docs/TASKS.md`；各产物使用说明见各自 README。
>
> 本工作区**只负责远控**：KernelSU 模块 `chopin-core`、零 root 客户端 APK `apps/pilot`、电脑侧 CLI `apps/pc-cli`。
> ROM/反编译/移植等研究资料已迁到 `/root/chopin-research/`，**不属于交付物，也不作为设计真源**；需要查证据时去那里看。
> 本工作区不再维护 `chopin-compat`、`miui14-agent-tune` 或其他 KSU 模块。
>
> 命名：**Sextant** 是项目展示名；技术标识（`chopin-core`、`chopin` CLI、`com.chopin.pilot`）暂保留。

---

## 1. 目标与范围

### 1.1 目标

在 chopin（Redmi Note 10 Pro 5G / POCO X3 GT）的 MIUI 14 上，提供一套**由电脑上的 agent 通过网络驱动手机**的完整能力，并且在锁屏状态下也能无人值守开工。

1. **一个设备端制品**：刷一个 KernelSU 模块 zip 即完成全部安装；模块自带守护进程、root helper、scrcpy-server、WebUI 静态资源，以及它负责安装的 APK。
2. **一个特权宿主**：模块开机以 root 拉起唯一守护进程（`chopin-core daemon`），全部特权语义（a11y 绑定与保活、MIUI 后台/doze/权限、解锁、注入、截图、会话、状态、审计）都在这个进程里实现。
3. **零 root 客户端**：APK 只做 UI / HUD / IME，不申请 KernelSU root、不执行 `su`、不拉起引擎、不写 allowlist 与无障碍 settings。
4. **状态可查**：守护把真实状态写成结构化 JSON 并提供 `/v1/status`；APK 与电脑都读结构化结果，不解析任何 CLI 文本。
5. **可驱动**：电脑侧交付 `chopin` 命令行客户端，一条 `adb` 命令完成首次配对，之后一组子命令覆盖全部能力。
6. **能力不降级**：未实现或不可用的能力以结构化错误失败，不做假成功、不做只读兜底、不做静默降级。

### 1.2 范围

- 在现有 MIUI 14 上做**可回退的远控定制**：KernelSU 模块 + 零 root APK，不刷镜像、不做源码级系统重建。
- 设备端只交付 `modules/chopin-core`；电脑端只交付 `apps/pc-cli`；客户端只交付 `apps/pilot`。
- 设备可回退：不丢用户数据、不 relock BL、不刷未验证镜像、不破坏 KernelSU/ADB 救援路径。
- 需要理解目标应用或 ROM 行为时，可以查阅 `/root/chopin-research/` 下的反编译/研究资料；这些只是证据，不是产品真源。

### 1.3 非目标

- 不做完整 AOSP/LineageOS 源码构建（本机容量不足）。
- 不做 ROM 移植、镜像打包、分区刷写、boot/vbmeta 修改或预装应用清理。
- 不在本工作区维护其他 KSU 模块（`chopin-compat`、`miui14-agent-tune` 等已移出）。
- 不绕过第三方安全产品、不伪造/分发 keybox、不泄露 ADB 私钥或用户数据。
- 不把 `/root/chopin-research/` 下的静态反编译结论当作当前运行时事实。

---

## 2. 当前基线

| 项 | 值 |
|---|---|
| 设备 | chopin / Redmi Note 10 Pro 5G（POCO X3 GT），<device-model> |
| SoC | MediaTek MT6893（Dimensity 1200），Mali-G77 MC9 |
| 系统 | MIUI 14 国行 `<rom-build>`，Android 13 |
| 分区 | 虚拟 A/B，super group `main`，stock system/vendor/product/system_ext 为 ext4 |
| BL | <bootloader-state> |
| Root | KernelSU 3.1.8 |
| 设备端制品 | `modules/chopin-core`（唯一交付物，见 §3） |
| 客户端 | `apps/pilot`（APK：状态页 + HUD + IME，零 root）、`apps/pc-cli`（电脑侧 `chopin` 命令） |
| ADB | `<adb-serial>`；授权 key 在 `adbhome/`（敏感，不提交） |
| 工作主机 | 本机构建环境：`/root/chopin-ksu` 工作区；研究资料在 `/root/chopin-research/` |
| 版本管理 | Git `main`，只跟踪远控源码/脚本/配置/当前文档；大文件、镜像、APK/JAR、下载和凭据忽略（凭据例外见 D26） |

### 2.1 工具链（本机，均被 `.gitignore` 忽略）

- Go：`tools/go/bin/go`（守护进程与 PC CLI 交叉编译）。
- Android SDK：`tools/android-sdk/{build-tools/android-35, platforms/android-34}`；`d8`/`aapt2`/`zipalign`/`apksigner`。
- JDK 17+（`javac`）、`kotlinc`（HUD）、`zip`/`unzip`。
- scrcpy-server v4.1：`apps/pilot/lib/scrcpy-server-v4.1`（不进 Git，构建脚本给出获取方式）。
- 不用 Gradle：APK 由 `apps/pilot/build.sh` 走 aapt2 → javac/kotlinc → d8 → 打包 → zipalign → apksigner。

### 2.2 工作区边界

- 仓库只保留远控主线：`modules/chopin-core`、`apps/pilot`、`apps/pc-cli`、当前文档、构建/验收脚本。
- 非远控资料统一放在 `/root/chopin-research/`：
  - `decompiled/`：JADX/APKTool 反编译树与索引；
  - `downloads/`、`extract/`：官方 ROM zip、payload/分区镜像；
  - `old/`：旧设备/旧构建归档；
  - `research/`：LineageOS、XMAPort、Shizuku、SukiSU allowlist、dsh-go、scrcpy 等调研；
  - `audit/`：设备侧环境审计截图/XML；
  - `tools/`：JADX、magiskboot、APK 样本与反编译脚本。
- 外部资料只读；需要新增反编译时，产物写回 `/root/chopin-research/`，不写进本仓库。

---

## 3. 交付物与目录

```
modules/chopin-core/          # 唯一设备端制品（KernelSU 模块 zip）
├─ module.prop
├─ customize.sh                  # 安装时权限、目录、可选预装 APK
├─ post-fs-data.sh               # 只做无 framework 依赖的准备（目录/权限/日志轮转）
├─ service.sh                    # boot_completed 后以 root 拉起守护；不承载语义
├─ uninstall.sh                  # 停止守护、卸 APK、还原写入、清理运行目录
├─ bin/chopin-core               # Go 单二进制：daemon 子命令 + ctl 子命令
├─ lib/helper.jar                # root app_process helper（binder/a11y/通知/SMS/定位/音频）
├─ lib/scrcpy-server.jar         # 虚拟屏/取帧
├─ webui/                        # /ui 静态资源（embed 在 Go 源码里）
├─ config/core.json              # 唯一配置（单目标、声明式）
├─ config/unlock-secret          # 图案凭据（0600，本地构建输入，不入 Git）
├─ src/                          # Go 源码
└─ tests/                        # 测试沙箱目录（当前只有 .gitignore 与遗留 allowlist-tool）

apps/pilot/                     # 零 root APK（UI / HUD / IME）
├─ app/                          # Activity + HUD(a11y) + IME + 前台服务 + helper Java 源
├─ build.sh                      # 构建（aapt2/javac/d8/apksigner）
└─ dist/chopin-pilot-<version>.apk  # 产物（ignored）

apps/pc-cli/                     # 电脑侧命令行客户端（Go）
├─ main.go / cmd/chopin/         # 子命令、验收矩阵、journey
└─ README.md

docs/DESIGN.md                   # 唯一设计真源（本文件）
docs/TASKS.md                    # 任务切片与真机验收记录
docs/ACCEPTANCE.md               # 验收流程与判定规则
README.md                        # 索引
AGENTS.md                        # agent 协作宪法
modules/chopin-core/README.md # 模块使用说明
apps/pilot/README.md            # APK 使用说明
apps/pc-cli/README.md            # PC CLI 使用说明
```

### 3.1 设备运行时目录（统一到一处）

`/data/adb/chopin-core/`

| 路径 | 内容 | 权限 |
|---|---|---|
| `config/core.json` | 运行时配置（首次从模块复制，升级不覆盖用户改动） | 0600 |
| `config/unlock-secret` | 图案凭据 | 0600 |
| `state/status.json` | 状态真源（原子替换写） | 0600 |
| `state/engine.lock` / `engine.pid` | 单实例锁与 pid | 0600 |
| `state/sessions.json` | 会话/roster/guard 持久化 | 0600 |
| `state/tokens.json` | token 记录（只存哈希） | 0600 |
| `state/pairing.json` | 一次性配对码（哈希 + TTL） | 0600 |
| `state/baseline/` | 写入前基线（回滚依据，保留供审计） | 0700 |
| `log/core.log` | 审计与运行日志（轮转，上限 1 MiB） | 0600 |
| `app/` | 给 APK 读的副本（`status.json`、`api.json`、`app-token`），chown 到 app uid | 0700 |
| `tmp/` | 中间产物 | 0700 |

旧路径 `/data/adb/chopin-remote/` 不再使用；`uninstall.sh` 与首次安装会迁移其 `secrets/screen-pattern` 到 `config/unlock-secret` 后清理旧目录。

---

## 4. Owner 划分与进程模型

### 4.1 Owner 表

| 层 | owner | 职责 | 禁止 |
|---|---|---|---|
| 设备端守护 | `modules/chopin-core/src`（Go） | 全部特权语义：a11y 绑定/保活、MIUI 后台/doze/权限、解锁、输入注入、截图/UI 树/OCR、会话与锁、虚拟屏、文件/设置/应用/通知/短信/定位、触发器、WebRTC、音频、审计、状态真源 | 不做 UI 渲染；不写 APK 私有目录（除发布给 APK 的副本） |
| 模块 shell | `modules/chopin-core/*.sh` | 开机准备目录与权限、按需安装内置 APK、拉起/停止守护、日志轮转 | 不写 settings/appops、不判定能力状态、不遍历目标清单、不解释授权 |
| 客户端 APK | `apps/pilot` | 状态页（读 `app/status.json`）、HUD 叠加层、IME、前台服务（订阅状态） | 不申请 root、不执行 `su`、不拉起引擎、不写 allowlist、不写 `enabled_accessibility_services` |
| PC 客户端 | `apps/pc-cli` | 配对、健康检查、按能力调用 `/v1`、导出 OpenAPI、验收矩阵 | 不承载业务判断；不缓存明文凭据 |

**单一真源**：跨入口复用的语义一律在守护里实现一次；shell、APK、PC CLI 只做协议映射与展示。APK 与 PC 都不解析 CLI 文本，只读结构化状态。

本工作区不再有 ROM 行为修正模块、第二套 a11y 保活或第二套状态真源。

### 4.2 进程与启动顺序

```
开机
 └─ post-fs-data.sh            # /data 可写、framework 未起：只建目录、权限、日志轮转
     └─ service.sh             # 等 sys.boot_completed=1（不等待 CE/解锁）
         └─ exec chopin-core daemon
             ├─ flock state/engine.lock（单实例，抢不到即退出并记日志）
             ├─ 起 TLS 服务 0.0.0.0:8443（Bearer + 证书 pin）
             ├─ 首轮状态发布 → state/status.json + app/ 副本
             ├─ 确保 APK 已安装（`pm install -r`，版本不一致时才装）
             ├─ 保活循环（ticker，默认 15s）：a11y 真实 bound、后台策略、权限组
             ├─ 解锁检查（锁屏且有凭据 → 自动解锁；单次尝试，不重试）
             ├─ helper（root app_process）按需拉起：UiAutomation 必须带
             │    FLAG_DONT_SUPPRESS_ACCESSIBILITY_SERVICES(1)
             └─ ctl 子命令（同一 ELF）：status / pair / token / secret / audit / apply / log
```

- 守护**不等待 CE、不等待解锁**：`health`/`status`/`device`/`input` 在锁屏下必须可用。
- 需要 CE 的动作（启动 Activity、读 app 私有目录、HUD）在解锁后执行；未就绪时返回结构化 `pending`，不假装成功。
- 守护自身被 MIUI 杀掉：`service.sh` 拉起的是常驻进程，若被杀由 KernelSU 的 service 机制与自身 `flock` + 父进程守护兜底；不得双开。

### 4.3 数据流

```
PC(agent)  ──HTTPS+Bearer──▶  守护 :8443  ──▶ helper(root) / scrcpy / settings / appops / pm / input
浏览器 /ui ──HTTPS+Bearer──▶      │
APK(零root) ────────────▶ 读 state/app/status.json（只读副本）
          └──HTTPS+app token(loopback)──▶ /v1/hud/*、/v1/status
HUD(a11y) ──SSE──────────▶ 守护 /v1/hud/events
IME(provider) ◀──content call── 守护（只接受 uid 0/2000/自身）
```

---

## 5. 能力矩阵（用户圈定范围）

全部保留，按切片实现（见 §11 与 `docs/TASKS.md`）。精确路由以 `modules/chopin-core/src/internal/engine/routes.go` 和 `/doc` 为准。

| 组 | 能力 | 切片 |
|---|---|---|
| 设备控制 | 设备信息/状态/锁态/唤醒/解锁/本机解锁/SIM PIN/锁定/屏幕/旋转/飞行/WiFi/重启 | S2 |
| 会话与锁 | 列出/claim/心跳/释放、roster、guard、reap、后台回收（D35）、任务收尾锁屏与异常 180s（D36）、活动信号与僵尸回收（D37） | S4/S24/S25/S26 |
| 虚拟屏 | 列出/创建/销毁 overlay display（scrcpy `new_display`） | S4 |
| 输入注入 | tap/swipe/key/text/longpress/global + `ime/{text,key}` + `ime` 状态 | S2/S6 |
| UI 感知 | screenshot/frame/tree/ocr/observe/find/wait/chain/color_probe/pixel_stats/panel_read/wait_result/events(SSE) | S2/S3/S16 |
| 通用 UI 原语 | observe/act/list/options/readback/page/wait_stable（D29） | S18 |
| 应用管理 | 列出/前台/launch/deep-link/stop/install/uninstall/clearData/permissions | S5 |
| 文件与上传 | 列表/下载/删除/移动、上传三阶段 | S5 |
| 设置与系统 | settings/{ns}/{key}、settings/daily、props、components enable/disable、watchdog | S5/S10 |
| 通知/短信/定位 | 通知列表/SSE/dismiss、短信列表/发送/状态、定位 | S5 |
| 音频 | 状态/话筒静音/扬声器路由/采集 RMS/播放 | S12 |
| 触发器 | CRUD、runs、all runs、SSE（timer/notification/sms/logcat） | S6 |
| WebRTC | offer/ice/close、stats（主屏按需镜像 + 虚拟屏） | S4 |
| WebUI 与文档 | `/ui`、`/doc`（OpenAPI）、`/doc/skill`（agent 手册与可安装包） | S6/S17 |
| HUD | heartbeat/pause/stop/ack/events/state（loopback 限定） | S6/S16 |
| 无人值守 | 锁屏下自动解锁并继续作业 | S2+S7 |
| 初始化 | `chopin init`、ctl secret set-unlock/set-sim、动画基线还原 | S17 |

---

## 6. 架构决策（ADR）

### 6.1 当前决策

#### D20：设备端单制品 —— `chopin-core` 模块即全部交付物

- 决策：设备端只交付**一个 KernelSU 模块 zip**。模块内同时携带 root 守护（Go）、root helper（dex）、scrcpy-server、WebUI 静态资源，以及它负责安装的客户端 APK。
- 原因：用户判定多模块/多层分离在单设备单用户场景下只增加层数与排查成本，且制造两套 owner 抢同一个 a11y settings 键。单制品把链路压到「模块→守护→系统」三跳，安装与升级只有一个步骤、只有一个版本号。
- 影响：
  - `modules/chopin-core` 是唯一设备端实现；其他 KSU 模块不在本工作区维护。
  - APK 不再是独立安装步骤（仍可手动安装，但由模块负责安装/升级）。
- 回退：卸载模块 + 重启即回到「未安装」等价的设备状态（写入项按基线还原，APK 一并卸载）。
- 验收：模块 zip 内含 daemon/helper/scrcpy/webui/APK；刷一个 zip + 重启后全部能力可用。

#### D21：root 宿主改为模块 —— 取消 KernelSU allowlist 授予层

- 决策：`service.sh` 以 root 直接拉起守护（KernelSU 以 root 运行模块脚本，SELinux 域 `u:r:su:s0`，uid 0，已在设备上实测确认）。**删除** 整个 root 授予层：`allowlist-tool`、`lib/root.sh`、`PENDING_REBOOT` 状态机、装载时写 `.allowlist` 的逻辑、以及「装模块后要重启一次才拿到 root」的装机契约。
- 原因：既然守护进程由模块以 root 拉起，APK 就不需要 root；没有 root 授予，`on_post_fs_data()` 只读一次 allowlist 这条内核限制也不再是设计约束。
- 影响：
  - APK 不再出现在 KernelSU 授权列表；`su` 调用从 APK 中全部删除。
  - 装机流程从「装模块 → 重启 → 装 APK → 再重启」简化为「刷 zip → 重启一次」。
  - `root_status`/`PENDING_REBOOT`/`allowlist` 解析相关代码与测试全部删除。
- 回退：无写入面（不写 allowlist），无残留。
- 验收：`ps -A` 显示守护 uid 0、`/proc/<pid>/attr/current` 为 `u:r:su:s0`；APK 在 KernelSU 授权列表里不存在，且全部特权功能仍可用。

#### D22：守护用 Go 单二进制（daemon + ctl 同一 ELF）

- 决策：`bin/chopin-core` 是一个 Go 静态 ELF，`chopin-core daemon` 跑服务，`chopin-core ctl <cmd>` 做本地运维（status/pair/token/secret/audit/apply/log/version）。shell 只剩 5 个开机生命周期脚本。
- 原因：旧实现里 shell 承载了状态判定、清单遍历、配置解析与保活决策，与 Go 引擎各有一套状态语义，导致「配置改了没生效」「两处判定不一致」这类故障。把全部语义收进一个进程，一处实现、一处测试、一处日志。
- 影响：删除旧 shell 语义层。
- 回退：shell 只做进程管理，删掉不影响设备状态。
- 验收：`sh -n` 通过；仓库内不存在被删的旧 shell 层；守护与 ctl 在同一 ELF 内（`chopin-core version` 与 daemon 同版本）。

#### D23：状态真源 = `state/status.json` + `/v1/status`

- 决策：守护维护唯一状态对象，写入 `state/status.json`（root 真源，0600，原子替换），并向 APK 私有可读目录发布副本（chown app uid）。`GET /v1/status` 返回同一对象（带 token 校验）。APK 读文件，PC 读 API，二者字段完全一致。
- 原因：旧契约让 APK 在 root 下执行 CLI 并解析文本输出（`key : value` 行），既脆弱又把 CLI 变成 API。结构化 JSON + 版本化 schema 让两侧都可校验，也消灭了「文本格式一变 App 就瞎」。
- 影响：APK 的 `HarnessClient`/`HarnessStatus` 文本解析器删除；`check-contracts` 改为校验 JSON 键集合与 schema 版本在守护、APK、PC CLI 三处一致。
- 回退：状态是只读派生数据，删除副本无副作用。
- 验收：设备上 `cat state/status.json` 与 `curl /v1/status`、APK 状态页三处字段一致；schema 版本不匹配时三方都显式报错。

#### D24：首次鉴权走 adb 配对

- 决策：不含任何预置凭据。首启时守护生成一次性配对码（哈希 + 5 分钟 TTL 存 `state/pairing.json`）。电脑侧 `chopin pair --serial <adb>` 通过 `adb shell su -c 'chopin-core ctl pair new --agent pc'` 取到码，再 `POST /v1/pair` 换取长期 API token（设备只存哈希；PC 侧存 `~/.chopin/config.json` 0600）。
- 原因：用户明确要「adb 连接手机，然后通过 adb 授权」。这使首次鉴权依附于已经可信的通道（adb + su），不需要在设备上手工抄 token，也不需要把 token 预置进仓库。
- 影响：配对码一次性、失败 3 次锁定并审计；`token rotate|revoke|list` 走 ctl。
- 回退：删除 `state/tokens.json` 即吊销全部 PC 客户端；APK 的 app token 由守护自动重建。
- 验收：清空 tokens → `chopin pair` 成功 → `chopin health` 通过；重放旧配对码被拒；错误码 3 次后锁定。

#### D25：能力不降级

- 决策：未实现、不可用或被 ROM 拒绝的能力，一律返回结构化错误（`code` + `message` + `detail`），不提供只读兜底、假成功、空实现、静默降级。`unsupported` 仅用于「有 ROM 证据证明该 ROM 不提供该能力」。
- 原因：用户明确「不需要回滚和降级，我要正正经经的做完」。此前的 `A11Y_FORCE_RESTORE=0` 之类折衷开关是把未解决的设计问题藏进配置。
- 影响：配置里不再有「失败时退化为 X」的开关；失败路径必须可观测（审计 + 状态 + 响应）。
- 回退：不适用。
- 验收：每个能力的失败路径都有真机证据（例如无凭据解锁返回 `credential_missing`、无焦点输入框返回 `text_input_unavailable`）。

#### D26：单用户凭据文件（工作区本地文件，不入 Git）

- 决策：图案解锁凭据保存在工作区文件 `modules/chopin-core/config/unlock-secret`（0600，属主 root），由 `.gitignore` 排除，**不提交到 Git**；构建模块时由 `build-module.sh` 校验存在并打进模块 zip。运行时只从该文件读取。
- 原因：用户要求「直接放工作区那个密码就行了」；但这不等于把真实解锁图案写进不可撤销的 Git 历史。单设备单用户场景下，文件 + 构建时打包已经满足可复现，且避免凭据随仓库扩散。
- 边界（不可放宽）：运行时凭据不进 argv（`ps` 全机可见）、不进日志、不进 API 请求/响应、不进审计；`lock-state` 类接口只返回「存在/不存在」；文件权限 0600、属主 root；模块 zip 是唯一携带凭据的产物，zip 本身不进 Git。
- 回退：删除该文件则自动解锁不可用，只能人工解锁。
- 验收：`git ls-files modules/chopin-core/config/unlock-secret` 为空；`git check-ignore` 命中；缺少该文件时 `build-module.sh modules/chopin-core` 显式失败；zip 内含该文件；`ps`/日志/API 无明文。

#### D27：产品命名与 3.0 迁移（Chopin Core / Chopin Pilot）

- 决策：模块 id/name 改为 `chopin-core` / `Chopin Core`；软件包名/label 改为 `com.chopin.pilot` / `Chopin Pilot`；电脑侧命令仍是 `chopin`。
- 目标标识：
  - 数据目录 `/data/adb/chopin-core`；模块二进制与 `su` 软链 `chopin-core`；helper socket `@chopin-core-helper`。
  - APK 私有目录 `/data/user/0/com.chopin.pilot/files`。
  - a11y `com.chopin.pilot/.hud.RemoteA11yService`；compat `com.chopin.pilot/com.google.android.accessibility.selecttospeak.SelectToSpeakService`；IME `com.chopin.pilot/.ime.RemoteImeService`；IME provider `com.chopin.pilot.ime`。
  - priv-app `/system/priv-app/ChopinPilot` + `system/etc/permissions/privapp-permissions-chopin-pilot.xml`。
  - 模块 zip `chopin-core.zip`；APK `chopin-pilot-<version>.apk`；版本 `3.0.0`。
- 原因：`harness` 是旧工程名，`remote` 过于泛化。Core 表示设备端特权核心（守护/helper/scrcpy/WebUI/APK 安装），Pilot 表示人/agent 的控制面；模块和软件各有一个名字，CLI 保留 `chopin` 作为统一入口。
- 不动的名字：`com.google.android.accessibility.selecttospeak.SelectToSpeakService` 这个类名必须保留（微信白名单靠它）；`X-Chopin-*` 请求头、`~/.chopin` CLI 配置目录、`chopin` 命令保留。
- 迁移（已于 2026-11-18 一次性完成，不留双版本）：迁移时从旧目录复制过 `config/unlock-secret`、`state/tokens.json`、`pki/*`，随后卸载旧模块、删除旧包 `com.chopin.remote` 与旧数据目录。当前代码**不再包含旧模块/旧路径兜底**：`customize.sh` 只播种自己的 `core.json`/`unlock-secret`，`service.sh` 只处理 `com.chopin.pilot`。
- 迁移硬约束（真机）：
  1. **priv-app overlay 不能单独运行**：这个 ROM 的 App 进程读不到 `/system/priv-app/ChopinPilot/ChopinPilot.apk`（ResourcesManager/ziparchive ENOENT → ContextImpl Resources NPE）。必须同时保留 `/data/app` 更新副本；`service.sh` 在只有 priv-app 路径时执行 `pm install -r`，包保持 `PRIVILEGED` + `UPDATED_SYSTEM_APP`。
  2. **CE 不可用时的发布重试**：守护先于首次解锁启动，`/data/user/0/com.chopin.pilot` 还不可用；`publishAppFiles` 必须在 keepalive 中重试，解锁后发布 `status.json`/`api.json`/`app-token`/`hud.json`，否则 HUD 永远不 provision。
  3. **HUD 读权限**：`GET /v1/hud` 接受 `read` 或 `hud`（均 loopback 限制）；写通道仍只接受 `hud`。只接受 `read` 会让 HUD 自己的 token 读回 403。
- **无回退路径**：旧模块/旧 APK 备份已删除；升级只支持 `chopin-core` 自身版本前进，不再保留双版本或旧包回退。
- 验收：`ksud module list` 只有 `chopin-core`；`pm list packages` 有 `com.chopin.pilot` 且无 `com.chopin.remote`；`dumpsys package com.chopin.pilot` 含 `PRIVILEGED`；a11y component/compat 都 `hasBound=true`；`chopin health/status` 正常；`chopin acceptance` PASS；journey 中微信、IME、HUD 通过。

#### D28：低延迟观测、HUD ACK 与一条命令初始化（2026-11-18）

- 背景（真机实测）：`/v1/ui/screenshot` 1509ms（`screencap -p` 中约 1.13s 是 PNG 编码），`/v1/ui/ocr` 913ms，`tap` 324ms（HUD 固定 settle 250ms + `input` 进程 70ms），`wait` 每 300ms 轮询一次树。agent 的每个「看→想→点」循环都被这些固定开销支配。
- 决策：
  1. **单帧捕获统一走 helper**：display 0 的截图/像素统计/OCR 一律使用 helper 的 `UiAutomation.takeScreenshot()`，JPEG 编码（默认质量 80），默认按原分辨率；`max_size` 可让 frame/observe 的编码缩放。不再用 `screencap -p` 做 agent 感知。虚拟屏仍走 scrcpy/scrcpy `screencap`，helper 只支持 display 0。
  2. **`GET /v1/ui/frame` 返回图片字节**：`image/jpeg`，参数 `quality`、`max_size`（缺省表示原图）、`cached`；响应头带 `X-Chopin-Frame-Ms`。这是 agent 的快速看屏入口，一次往返拿到像素。
  3. **`GET /v1/ui/screenshot` 保持 JSON**：默认 JPEG，响应增加 `format`、`width`、`height`、`encode_ms`；文件路径与 `bytes` 语义不变。
  4. **新增 `POST /v1/ui/observe`**：接受 `tree`/`ocr`/`pixels`/`frame` 四个选择与 `max_size`、`quality`，一次截帧后在 helper 内完成所选观测，返回同一帧的结果与 `timings`（`capture_ms`/`tree_ms`/`ocr_ms`/`encode_ms`）。这是减少重复截帧与往返的主入口。
  5. **复用最近帧**：`/v1/ui/ocr`、`/v1/ui/pixel_stats`、`/v1/ui/color_probe` 默认使用 TTL 2s 的最近帧，`fresh=true` 强制重截。TTL 内没有可用帧时才截新帧。
  6. **2fps 帧缓存**：守护在 display 0 维护最近一帧 JPEG（内存上限 4 MiB），`frame?cached=1` 直接返回；无缓存时按需截帧。缓存只在有读取时维持，不引入永久后台线程。
  7. **HUD ACK 驱动**：capture/pass-through 指令带 `request_id`，HUD 应用后调用 `POST /v1/hud/ack`（loopback，`hud` scope）；守护收到 ACK 立即继续，超时（`hud.capture_settle_ms`）才按原固定等待继续。没有 HUD 连接时不等待。ack 端点不改变 HUD 的暂停/停止语义。
  8. **事件驱动等待**：`wait` 优先由 a11y 事件唤醒，事件流不可用时回退 250ms 轮询；合同不变。
  9. **初始化**：新增 `chopin-core ctl secret set-sim`（stdin，写 `config/sim-pin`，0600）与 `chopin init`：health → 未配对时 pair → 图案凭据（stdin）→ 可选 SIM PIN（stdin）→ 可选关闭窗口动画（记录基线到 `state/baseline/`）→ 读 status。`chopin init --restore` 按基线还原动画；卸载也还原。
  10. **skill 下载**：`GET /doc/skill` 返回 Markdown 操作手册；`GET /doc/skill.zip` 返回带 `SKILL.md` 与脚本的可安装包；两者公开只读，与 `/doc` 同级。`/doc` 的 OpenAPI 用 `externalDocs` 指向 `/doc/skill`。PC CLI 加 `chopin skill [-o file]` 与 `chopin skill --install DIR`。
- 非目标：不引入 H.264 解码依赖（现有依赖只有 `pion/webrtc` 与 `modernc.org/sqlite`），因此不做「从 WebRTC 流直接取帧」；用帧缓存与 helper 快帧替代。虚拟屏快照不在本轮提速范围。
- 安全：凭据仍只经 stdin，不进 argv/日志/API/审计；skill 包不含凭据；动画基线写入 `state/baseline/` 并由 `--restore`/`uninstall.sh` 还原。
- 验收：真机 20 次采样的 P95——`/v1/ui/frame` ≤ 400ms、`/v1/ui/observe`（tree+frame）≤ 700ms、`tap`（HUD 已连接）≤ 150ms；`chopin acceptance` PASS 30 / FAIL 0 / SKIP 0，`chopin journey` PASS 20 / FAIL 0；清空 tokens 后 `chopin init` 一条命令完成配对与凭据录入。

#### D29：agent 体验契约 —— 观测帧、动作事务与任务配方（2026-11-18）

- 背景（本轮真机电商任务暴露的是体验问题，不是功能缺失）：
  - agent 看到的是碎片：树、OCR、截图来自不同时刻，a11y bounds 与 OCR 坐标相差 150px，SKU 的 `selected` 根本读不到；
  - 动作没有反馈：`find --click` 返回 `clicked=true` 但屏幕没动，失败没有原因，agent 只能重试；
  - 等待靠 `sleep`：CLI 没有 `wait`/`chain`，`/v1/ui/events` 还是空流；
  - 按文档写脚本会踩坑：`tree` 默认文本、`shot` 写 JPEG 却叫 PNG、skill 写了不存在的 `--fresh`；
  - 跨 App 任务没有编排，agent 每次重新发明“找订单 → 比价 → 分享”的流程。
- 决策（体验目标 → 合同）：
  1. **一眼看全（Observation Frame）**：一次捕获生成 `frame_id`，树/OCR/像素/JPEG 全部挂在同一帧，带 `captured_at`、`coordinate_space`、`truncated`。节点补 `node_id`、`parent_id`、`selected`/`visible`/`enabled`（三态：true/false/unknown）、`window_id`、`depth`。`find` 返回全部候选（`candidates`）与推荐目标，排序：匹配等级 → visible/enabled → 可点击或可点击祖先 → 屏幕内 → 面积最小。树截断必须显式 `truncated`。
  2. **做了就知道有没有用（Action Transaction）**：新增 `POST /v1/ui/act`，`target` 用 `node_id`/selector/`bounds`，支持 `expect` 与 `verify_ms`。返回 `dispatched`、`channel`、`clicked_bounds`、`observed`（动作后的新帧摘要）、`verified`（true/false/unknown）、结构化 `failure`（`target_not_found`/`target_disabled`/`offscreen`/`ambiguous`/`no_effect`/`verify_timeout`）。不自动重试；`unknown` 不冒充成功。
  3. **不用猜什么时候稳定（wait_stable）**：新增 `POST /v1/ui/wait_stable`，先等 a11y 事件静默 `stable_ms`，再比较树/区域的规范化 hash；超时返回结构化失败。现有 `wait`/`wait_result` 保留，统一走同一套匹配器。
  4. **失败能直接指导下一步**：`failure.code` 必须能映射到动作（`ambiguous` → 用 `node_id` 重试；`no_effect` → 换通道/坐标或停止；`target_disabled` → 换候选或停止）。审计记录目标文本与 `frame_id`，不记裸坐标。
  5. **CLI 与文档一致**：补 `wait`/`chain`/`wait_result`、`find --id`、`ocr --fresh`、`tree --summary` 说明、`shot` 按实际格式命名、`launch --clear-task`；未知 flag 必须报错，不静默吞掉。`/doc/skill` 只写真实存在的参数。
  6. **任务可复现（Task Recipe）**：`/doc/skill` 定义配方格式（前置检查、步骤、每步 `expect`、失败策略、停止条件、发送后回读）；PC CLI 新增 `chopin run recipe.json`，在电脑侧执行配方并调用守护原语。业务语义留在 CLI/配方，不进守护（D22 边界）。
  7. **底层一致性**：`/v1/ui/events` 真正订阅 helper 事件；事件 start/stop 引用计数，`wait` 可重启；所有注入路径（tap/swipe/longpress/chain/find 回落）统一走 action guard（HUD pass-through 引用计数、捕获互斥）；截图/OCR/observe 的临时文件按请求唯一命名。
- 非目标（本轮明确不做）：
  - 结算安全闸暂不处理（用户决定）；不在守护内做订单/商品搜索原语；不做 ACTION_SEND 通用化；不把 chain 扩成跨 App DSL；helper `injectInputEvent` 只作为延迟优化，不作为点击正确性方案。
- 验收（agent 体验指标）：
  - 一次 `observe` 能拿到决策所需的树+帧；一次 `act` 能回答“点没点中、是否生效、为什么没生效”；
  - `wait_stable` 在真机替代 `sleep`，失败有原因；
  - `chopin run` 能复现“订单 → 同款同码 → 比价 → 分享”的只读流程，步骤可审计；
  - `chopin acceptance` / `journey` 覆盖 `act`/`observe`/`wait_stable`/`run`；CLI 命令与端点一一对应由静态门禁锁定。

#### D29：agent 体验契约 —— 七个通用 UI 原语（2026-11-18）

- 背景（本轮真机任务暴露的是 agent 使用系统时的通用体验缺陷，不是“缺某个电商功能”）：
  - 感知碎片化：树/OCR/截图来自不同时刻，a11y 与 OCR 坐标实测差 150px，`selected` 读不到，树截断不可见；
  - 动作不可验证：`clicked=true` 但页面没动，失败没有原因，agent 只能重试或猜；
  - 等待靠 `sleep`；`/v1/ui/events` 是空流且会永久停掉 `wait` 的事件源；
  - 列表、选项组、结果回读没有结构化原语，agent 只能逐个 `find` + 推断；
  - 文档与 CLI 不一致（`tree` 默认非 JSON、`shot` 写 JPEG 却叫 PNG、skill 写了不存在的参数）。
- 决策：把跨 App/跨任务的共同问题收敛为七个通用原语，所有场景复用；业务语义（比价、订单、分享、设置）留在 CLI 配方，不进守护（D22 边界）。
  1. **`POST /v1/ui/observe`（观测帧）**：一次捕获生成 `frame_id`，树/OCR/像素/JPEG 同帧、同坐标系，带 `captured_at`、`truncated`。节点补 `node_id`、`parent_id`、`selected`/`visible`/`enabled`（三态 true/false/unknown）、`window_id`、`depth`。`find` 返回 `candidates` 与推荐目标，排序：匹配等级 → visible/enabled → 可点击或可点击祖先 → 屏幕内 → 面积最小。
  2. **`POST /v1/ui/act`（动作事务）**：`target` 用 `node_id`/selector/`bounds`，支持 `action`（click/longpress/set_text/scroll/select）、`duration_ms`、`expect`、`verify_ms`。返回 `dispatched`、`channel`、`clicked_bounds`、`observed`（动作后新帧摘要）、`verified`（true/false/unknown）、`failure`（`target_not_found`/`target_disabled`/`offscreen`/`ambiguous`/`no_effect`/`verify_timeout`/`danger_button`）。不自动重试。
  3. **`POST /v1/ui/list`（列表提取）**：按 item selector + 字段集提取重复项，支持 `dedupe_by`、`max_items`、可选有界滚动续取（`max_scrolls`）。返回 `items`、`count`、`truncated`、`scrolls`、`page_fingerprint`。覆盖商品/订单/联系人/通知/聊天。
  4. **`POST /v1/ui/options`（选项选择）**：读取当前选项组的 label/options（`text`、`selected`、`enabled`、`visible`、`bounds`）；配合 `act(select)` + `expect.selected`。覆盖 SKU、下拉、菜单、设置单选。
  5. **`POST /v1/ui/readback`（结果回读）**：按 selector/region 在 tree/OCR/pixels 中回读，返回 `found` 与 `matches`。用于发送后读气泡、设置读回、搜索/列表结果确认；不以提示文案判断成功。
  6. **`GET /v1/ui/page`（页面状态）**：返回前台包/Activity、窗口与弹层类型、键盘状态与高度、`fingerprint`（结构与文本 hash）、当前 `frame_id`。用于起点判断、弹层识别与配方分支。
  7. **`POST /v1/ui/wait_stable`（稳定等待）**：先等 a11y 事件静默 `stable_ms`，再比较树/区域 hash；超时返回结构化失败与原因。替代 `sleep`。
- 底层修复（P0）：
  - `/v1/ui/events` 真正订阅 `SubscribeEvents` 并写 SSE；`EventsStart/Stop` 引用计数，`wait` 可在事件流重启后恢复；
  - 所有注入路径（tap/swipe/longpress/chain/find 回落）统一走 action guard（HUD pass-through 引用计数、捕获互斥）；
  - 截图/OCR/observe 的临时文件按请求唯一命名；
  - `find`/`act` 回传真实 `clicked_bounds` 与 `channel`，审计记录目标 selector 与 `frame_id`，不记裸坐标。
- CLI 一致性（P0）：补 `wait`/`chain`/`wait_result`、`find --id`、`ocr --fresh`、`page`、`act`、`list`、`options`、`readback`、`wait-stable`；`tree` 默认 JSON、`--summary` 保留人类摘要；`shot` 按实际格式命名；`launch --clear-task`；未知 flag 报错；`help --json`。
- 任务配方（P0）：`/doc/skill` 定义 JSON 配方（前置检查、步骤、每步 `expect`、失败策略、停止条件、发送后回读；零依赖，不引入 YAML 解析库）；`chopin run recipe.json` 在电脑侧执行，步骤直接使用上述原语；支持 `--resume` 与步骤级 trace（`frame_id`、`verified`、`timings`）。业务语义在配方，不在守护。
- P1（紧随）：输入安全（`set_text` 后回读、发送前校验、键盘 insets、危险按钮识别）；长任务 keep-awake 与断点/进度；上下文裁剪（`fields`/`max_items`/`summary`/`viewport_only`）；`chopin doctor` 与 `chopin log`；请求级 `request_id/frame_id/timings`。
- P2：剪贴板读取（敏感边界：只在内存返回、不进日志/审计、不落盘）；增量树 diff；metrics 端点；页面指纹版本检测与配方漂移告警。
- 非目标：不在守护内做订单/商品搜索等业务原语；不做 ACTION_SEND 通用化；不把 `chain` 扩成跨 App DSL；helper `injectInputEvent` 只作延迟优化，不作点击正确性方案；结算安全暂不处理（用户决定）。
- 验收（agent 体验指标）：
  - 一次 `observe` 拿到决策所需的树+帧+三态；一次 `act` 能回答“点没点中、是否生效、为什么没生效”；
  - `list`/`options` 一次结构化，`readback` 能确认发送/搜索结果；
  - `wait_stable` 在真机替代 `sleep`，失败有原因；
  - `chopin run` 能复现“订单 → 同款同码 → 比价 → 分享”的只读流程，步骤可审计；
  - `chopin acceptance`/`journey` 覆盖新原语；CLI 命令与端点一一对应由静态门禁锁定。

#### D30：拟人操作与验证码分流（2026-11-19）

- 背景（用户决策 + 外部证据）：
  - 用户决定引入**完整拟人模式**：点击落点随机、动作节奏抖动、同一目标限速，作为 agent 驱动时的默认路径。
  - PhoneGo（HKUDS）源码与实测文档给出两条可直接复用的结论：确定性坐标回放在被风控的 App 上会被识别；验证码必须分流处理，反复尝试只会让账号更危险。其 `risk.py` 记录「这个 App 弹过验证码」，7 天衰减，命中后不再回放宏。
  - 本项目当前完全没有验证码概念（守护与 CLI 零命中），遇到验证页会一直重试到预算耗尽。
- 决策：
  1. **拟人输入层归守护**（唯一注入 owner）。所有坐标注入（`tap` / `swipe` / `longpress` / `act` 坐标兜底 / `chain` 回落）统一经过该层；`act` 走 a11y `ACTION_CLICK` 时不产生触摸事件，不做落点抖动。
  2. **落点**：目标 `bounds` 内、四边各留 `1/tap_inset`（默认 6，最小 2px）后均匀取点；无 bounds 的裸坐标以 `±jitter_px`（默认 6）抖动；元素短边 <8px 时退化 `±3px`。不点元素正中心。
  3. **节奏**：同一 display 连续两次注入之间施加 `[gap_min_ms, gap_max_ms]`（默认 700–1600）的间隔，从上一次注入**完成**起算。这是限速与背压，不是伪装。
  4. **时序抖动**：手势时长按 `[wait_min_pct, wait_max_pct]`（默认 80–135）缩放；守护不替调用方插入等待，也不缩放调用方的 `wait`/`wait_stable` 超时。
  5. **种子化随机**：请求可带 `seed`（字符串）；不带时守护用 `crypto/rand` 生成 8 字节 hex，在响应 `seed` 字段与审计行返回。派生方式 `HMAC-SHA256(seed, "<display>|<request_id>|<step_index>")`，每次注入独立取值但同一 seed 可精确复现。`chopin run` 生成 run seed 写入 run 状态，`--seed` 复现同一 run；CLI 的步骤等待抖动用同一 seed 派生。
  6. **延迟指标重新定义**（与 D28 的关系）：D28 的 `tap ≤150ms` 改为 `dispatch_ms`（请求到达至注入下发，不含刻意节奏等待）；新增 `paced_ms` 单独报告。端到端动作间隔变长是设计目标，不是回归；验收同时断言 `paced_ms` 落在配置区间内。
  7. **验证码检测**：新增 `POST /v1/ui/challenge`，分类当前页为 `slider` / `code` / `none`。判定要求共现，不做单词命中；词表命中但没有对应控件时返回 `none` 并把命中词放进 `evidence`（搜索结果、帮助文章都长这样，把文字当墙会让 agent 在正常内容上停手）：
     - `slider`：滑块词表（拖动滑块 / 拖动下方滑块 / 滑动验证 / 拖动滑块完成拼图 / 完成安全验证 / 人机身份验证 / verify you are human）**且**存在可拖动节点（class 含 SeekBar / Slider，或 text/content-desc 含 滑块 / 拖动）。
     - `code`：验证码词表（短信验证码 / 邮箱验证码 / 请输入验证码 / 验证码已发送 / verification code）**且**存在可编辑节点（`editable="true"` 或 EditText）。
     - 搜索结果里出现「验证码」三个字不算：没有可编辑节点就不判 `code`。
  8. **验证码分流**：
     - `slider` → `POST /v1/ui/challenge/solve` 先自解。无 `dx` 时按「拖到轨道最右」尝试，最多 `slider_attempts`（默认 2）次，每次后重新分类；仍为 challenge 返回 `challenge_unresolved`，不冒充成功。带 `dx` 时按给定位移拖动（缺口位置由上层视觉给出，守护不做图像识别）。
     - `code` → 一律拒绝求解，返回 `challenge_needs_human`，由调用方进入 handover；守护不代填短信/邮箱验证码。
     - `none` → 不拦截；由后续动作的真实失败暴露问题。
     - `act` 验证失败（`verified=false`）时守护再分类一次当前页：命中验证页则把 `failure.code` 改为 `challenge_slider` / `challenge_code` 并附 `challenge` 证据；`code` 同时写入 risk。调用方即使漏了 `challenge` 步骤也不会继续锤同一个验证页。
  9. **风险记录**：`state/risk.json`（0600，原子写）按包记录 `{count, last, until}`，7 天衰减。`chopin run` 预检：`count≥1` 告警，`count≥3` 拒绝自动执行（`challenge_risk`），只允许显式 `--force`。
  10. **人工接管**：
      - `POST /v1/device/handover` `{package, reason}`：按 component 解析后 `am start --display 0` 把该包切到用户屏幕，置 handover 状态，返回 `{active, package, display}`。
      - handover 期间所有注入端点（tap/swipe/longpress/key/global/text/act/chain/find 点击）返回 409 `handed_over`，不静默放行。
      - `POST /v1/device/handover/return` 清除状态；`launch_on_agent_display=true` 时把包重新拉到 agent 的 display。
      - `GET /v1/device/handover` 查询当前状态。
      - 审计只记开始/结束与包名，不记 `reason` 全文（可能含用户描述）。
  11. **配置**（§7 增加 `human` 与 `challenge` 两节）：`human.enabled` 默认 true；`human.enabled=false` 时完全确定性（基准测试与真机 A/B 用）。数值校验：`gap_max_ms ≥ gap_min_ms`、`tap_inset ≥ 1`、百分比 50–200，越界回落并记 WARN。
- 非目标：
  - 不做随机 UA、传感器注入、贝塞尔滑动、触摸压力曲线；不做操作顺序随机化。
  - 不做图像识别缺口（缺口位置由上层视觉给出 `dx`）；不做短信/邮箱验证码自动填写。
  - 不在守护里缓存验证码状态机；风险记录只有包级衰减计数。
- 边界：
  - 拟人输入只作用于**本设备、本用户账号**的自动化，不改变 a11y 绑定、不伪造系统身份。
  - 滑块自解只处理「拖到最右」与「按给定 dx 拖动」两种形态；图像拼图缺口不在本轮范围。
- 验收：
  - 同一 `seed` 两次 `chopin run` 的 `point` 与 `paced_ms` 完全一致；不同 seed 的 10 次 tap 落点重复率 <10%。
  - 真机：设置页连续 10 次 tap 的 `interval_ms` 全部落在 `[gap_min_ms, gap_max_ms]`，`dispatch_ms` P95 ≤150ms。
  - 验证码页：滑块页 `challenge` 返回 `slider`，`solve` 成功或返回 `challenge_unresolved`（不许假成功）；短信验证码页返回 `code`，调用 handover 后注入端点全部 409。
  - 搜索结果页含「验证码」字样但无可编辑节点时，`challenge` 返回 `none`。
  - Go 单测覆盖：种子派生、落点边界、gap 计算、词表共现判定、handover 拦截。

#### D31：App 地图层（2026-11-19）

- 来源与授权：结构与算法移植自 PhoneGo（HKUDS，MIT License，Copyright (c) 2024 xuyifan-0731 / 2025 yangqin-jiang）。移植文件保留版权声明；本项目不复用其 Python 运行时、LLM 路由与 VLM 回退。
- 背景：D29 给了原语（observe/act/list/options/readback/page/wait_stable），但没有「同一 App 第二次走同一条路」的持久结构。PhoneGo 的实测说明地图层的价值在覆盖率而不是智能：设置地图从 10% 落点提升到 90%，靠的是启动校验与跨 App 污染拦截，不是模型。
- 决策：
  1. **地图归 CLI**：存 `~/.chopin/maps/<package>.json`（0600）。守护零新增状态；匹配、操作目录、相对宏、回放全部在 `apps/pc-cli`。符合 D22「CLI 只做协议映射 + 本地编排」。
  2. **编码照搬 PhoneGo 的 YAML**：字段与单位一致（`screens[].elements[].{text,center,leads_to,found_at_scroll,fixed,aliases,semantic_type,dynamic}`、`screens[].selected/description/package/scrollable`、`screen_macros`、`launch_intent`、`screen_w/h`、`display`、`crawled_at`、`bench{ops,exact,same_app,at}`、`coverage`、`known_limitations`；宏步骤的 `wait` 用秒、`duration` 用毫秒）。本机 Go 缓存无 YAML 包，因此把 `go-yaml/yaml v3.0.1` 以源码形式 vendor 到 `apps/pc-cli/third_party/yaml`（MIT + Apache-2.0，附 LICENSE/NOTICE），`GOPROXY=off` 仍可构建；这是本轮唯一新增的第三方代码。
  3. **不做 LLM 富化与 LLM 路由**：PhoneGo 的 `aliases`/`description`/元素分类与 `_route` 都调模型；本项目由电脑上的 agent 决策。CLI 输出**操作目录**（`map ops`）与**相对宏**（`map nav`），agent 自己选。地图里 `aliases`/`description` 为空，`known_limitations` 记 `no_llm_enrichment`。
  4. **屏幕识别**（照搬 `AppMap.identify_current_screen`）：按 `text`/`content-desc` 集合匹配，排序 `(overlap, coverage, rarity)`；`coverage ≥ 0.5`；`package` 过滤；`selected` tab 必须相交；`dynamic` 与 `found_at_scroll>0` 的元素不作为锚点；`rarity` 用「该文本出现在几张屏上」的倒数，仅用于打破平局。
  5. **操作目录**（照搬 `build_operations`）：从 `screen_0` 深度优先，`op_id` 由路径文本拼接；有 `leads_to` 为 `NAV`，否则 `ACT`；每条含从冷启动开始的宏；跳过噪声标签与单字符 ASCII；`text` 随 tap 步骤保存，供回放时先在实时树里对齐标签、再退回坐标。
  6. **相对宏**（照搬 `find_relative_macro`）：两条 `screen_macros` 去掉公共前缀，得到「当前屏 → 目标屏」的最短已知路径；无公共前缀时返回完整路径。
  7. **回放**（照搬 `MacroExecutor`，两处按真机证据修正）：`force_stop → launch`（按 component 解析，轮询前台确认落地，**随后等树稳定**——窗口先变、树后变，不等就会读到上一屏）→ 逐步 tap/longpress/swipe/text/back/home/key/wait。tap 带 `text` 时先 `act` 的 a11y 通道按文本点击；找不到时在**有界页数内滚动继续找**，最后才退回坐标。这一步偏离 PhoneGo 的「宏内写死滚动次数」：实测一次 `ACTION_SCROLL_DOWN` 在不同列表上的位移不同，按录制页数滚动会把点击落到相邻行（`声音与触感` 打开小米账号页）。屏幕校验默认**警告不中止**（PhoneGo 实测中止版 0/10、警告版 7/10），只有 `--strict-screen` 才中止。
  8. **爬取**（照搬 `_crawl_verified`）：BFS + 每步校验。守卫清单：启动校验（前台包名/指定文本，最多 3 次）、点击后落地校验、跨 App 污染拦截（按包配置的 `foreign_hosts`）、危险标签跳过（新建/删除/发送/支付/关注等）、系统弹窗先拒绝、相似度判重（结构键 Jaccard 与文本 containment 取大，`floor=4`）、已知屏跳过重采、瞬态元素过滤（两次 dump 取交集）、单屏最少 4 元素、时间预算、单步超时、增量落盘、无线电状态（WiFi/蓝牙/飞行）爬前记录爬后恢复、`Switch`/`CheckBox` 默认不点。
  9. **爬取边界**：本 CLI 是电脑侧驱动，与 PhoneGo 的宿主机爬虫同层，默认驱动 display 0 且**不再要求额外确认**（用户显式执行 `map build` 即视为同意真实点击）。`--display N` 预留给后续虚拟屏支持，当前 N>0 返回 `unsupported`。只爬 `--package` 指定的一个包；不遍历已安装应用。
  10. **CLI**：`map build --package P --app NAME [--display N] [--max-screens N] [--max-depth N] [--max-minutes M] [--scroll-pages N] [--out FILE]`；`map list`；`map show FILE`；`map ops FILE [--nav-only]`；`map nav FILE OP_ID [--from-screen ID]`；`map bench FILE [--limit N] [--strict-screen]`。
  11. **与配方的关系**：新增 recipe action `map_nav`，参数 `{map, op}`，由 CLI 解析相对宏并调用同一回放器。业务编排仍在配方；地图只是可复用的导航产物（照搬 PhoneGo「命令处理已知、视觉处理未知」的分层）。`/doc/skill` 增加「怎么用目录选操作」一节，把 PhoneGo 的 `MACRO_PLAN_PROMPT` 四选一规则（OP / MACRO_VLM / NEED_VLM / FINISH）改写成 agent 指引，不调用模型。
  12. **底层照搬（PhoneGo 有明确实现的三项，不涉及地图 API）**：
      - `POST /v1/apps/launch` 改为落地校验：`am start` 后轮询前台包名，未落到目标包返回 503 `launch_not_landed` 并带实际前台；成功返回 `landed` 与 `foreground`。原实现只看 `am start` 退出码，是 D25 意义上的假成功。
      - `pageFingerprint` 与 `treeHash` 纳入 `selected`：PhoneGo 的屏幕签名是原始树，天然包含 tab 选中态；不纳入会让同一 App 的两个 tab 指纹相同。
      - `dangerButtons` 补齐 PhoneGo `SPEND_WORDS` 中的 充值/打赏/续费/开通会员/确认订单。爬虫的宽词表已在决策 8 搬过；agent 侧仍保持「拒绝 + `allow_danger`」，不引入 PhoneGo 的交互式确认，因为 D13 明确不做审批队列。
- 已知限制（2026-11-19 首次真机实测）：
  1. 没有 LLM 分类，动态文本（用户名 `<user>`、SSID `<ssid>`）会进入操作目录；它们能导航，但不是稳定入口。
  2. 同一入口可能落到相邻行：实测设置地图里账号副标题 `账号、云服务、会员权益等` 记录到隐私页（`com.miui.securitycenter`）；用种子 1 抽样 bench 时 `<user>` 一项因此落到 screen_3，如实计为失败。
  3. 地图覆盖取决于爬取预算；5 屏浅图 + 动态内容多，bench 是抽样，不代表全量精度。
- 非目标：不做 LLM 富化/路由、不做 VLM 回退、不做跨设备地图同步、不做自动安装包遍历、不做任务级 planner。除 vendored `yaml.v3` 外不引入新依赖。
- 验收：
  - 单测：真实设备树夹具上的屏幕识别、操作目录、相对宏、相似度判重、危险标签守卫；同一夹具两次识别结果一致。
  - 真机：`map build` 在设置上产出 ≥4 屏地图且 `coverage.complete` 如实；`map bench --limit 3` 的 NAV 精确落点 ≥2/3；爬取前后 WiFi/蓝牙/飞行状态一致；`map nav` 能从当前页回到指定屏；`chopin run` 的 `map_nav` 步骤可执行。
  - 首次实测（2026-11-19）：设置 **19 屏、`complete=true`（队列耗尽）**、1080x2400；`--seed 1 --limit 5` 的 NAV 抽样 **exact 5 / same_app 5**；`map nav` 到 WLAN 与 `map_nav` 配方均成功；爬取前后 `wifi_on=1 / bluetooth_on=1 / airplane_mode_on=0` 不变；`chopin acceptance` PASS 40 / FAIL 0 / SKIP 0（含 challenge/risk/handover），`chopin journey` PASS 21 / FAIL 0（含地图建图与导航步骤）。
  - 格式兼容：直接 `Load` PhoneGo 自带的 `settings_android.yaml`（50 屏、1440x3120）并导出 282 个操作。
  - 底层三项单测：`waitForeground` 命中与错报实际前台；两个仅 `selected` 不同的树指纹不同、同一树指纹相同；消费词 充值/打赏/续费/开通会员/确认订单 均被拒。
  - 静态门禁锁定 CLI↔端点与地图字段名。

#### D32：agent 引导入口（`/doc` 的 `x-chopin-guidance`，2026-11-19）

- 背景：agent 的入口是 `/doc`，但当前只返回 OpenAPI，靠 `externalDocs` 再跳一次 `/doc/skill`；而 SKILL.md 的先决条件是“模块已刷好”，从零安装步骤只在仓库 README 里。结果是 AI 拿到接口清单却不知道第一步做什么。
- 决策：
  1. `/doc` 仍是 OpenAPI 3.1，顶层增加 `x-chopin-guidance`：`skill`/`skill_zip` 链接 + 分阶段步骤 `steps[]`，每步含 `id`、`phase`（`install` / `use`）、`title`、`command`、`expect`。
  2. 安装阶段按从零写：`ksud module install` 落盘 → 重启一次 → `chopin health` → `chopin init`（图案/SIM 只经 stdin）→ `chopin doctor`。
  3. 使用阶段：`status` → `observe` → `act` → `map where/ops/nav`（有地图时）→ `run` 配方。
  4. SKILL.md 增加「从零安装」章节，与 `x-chopin-guidance` 同步；两处都不含凭据原文，凭据命令一律 stdin。
  5. 引导内容是接口文档的一部分，随守护二进制发布；版本不一致时以响应里的 `info.version` 为准。
- 非目标：不做自动下载模块 zip、不做自动刷机、不把安装步骤变成可执行脚本；不新增第二套步骤来源。
- 验收：`GET /doc` 含 `x-chopin-guidance` 且 `steps` ≥ 6、`install` 与 `use` 两阶段都在；`GET /doc/skill` 含「从零安装」且 `init` 命令与 `/doc` 一致；单测锁定字段与阶段；真机 `curl -k /doc` 可读。

#### D33：S20 深度能力 —— metrics、指纹漂移与增量树 diff（2026-11-21）

- 背景：D29 P2 列了四项深度能力，但只写了名字：metrics 只有分散的 `timings`，地图只有 `selected` 没有指纹/版本，树没有 diff 且 `node_id` 是单次 dump 的前序位置、跨帧会整体平移。剪贴板读取曾列入本轮，**2026-11-21 撤销**：Android 10+ 只允许焦点应用或默认输入法读剪贴板，后台 helper 的 `getPrimaryClip()` 返回 null；该场景又与 tree/OCR 的感知高度重叠，保留端点只会长期返回不可用。实现前先把剩余三项的契约写死。
- 决策：
  1. **metrics 只在内存**：`GET /v1/metrics`，scope `read`。进程重启清零，不写入 `status.json`，不改 APK schema。固定字段：`uptime_sec`、`requests{total,status_2xx,status_4xx,status_5xx}`、`routes[]{route,count,avg_ms,p50_ms,p95_ms,max_ms}`、`ui{observe,act,acts_verified,acts_failed,failures{}}`、`helper{calls,errors,avg_ms}`、`frames{captured,cached_bytes}`。路由标签用 `r.Pattern` 的固定集合（去掉方法前缀），未登记路由归 `other`；每个路由保留最近 256 个样本的环形缓冲算分位，标签与内存有界。禁止把请求体、文本、token 放进任何字段。
  2. **指纹版本与漂移**：`/v1/ui/page` 增加 `app_version`（复用 `packageVersion`，按包名 30s TTL 缓存，缓存条数有界）。地图 `Screen` 增加可选 `fingerprint`、`app_version`；爬取建屏时写入。`map where/nav/bench` 识别后对比当前页指纹与版本，不一致输出 `drift` / `version_changed` 警告但不中断：地图识别仍有文本重叠证据，不是假成功。配方 `map_nav` 步骤可选 `expect_fingerprint`，不一致按步骤失败处理（走现有 `expect` 语义）。`pageFingerprint`（不含 bounds）用于页面身份；`treeHash`（含 bounds）只服务稳定等待，两者不混用。
  3. **增量树 diff**：`POST /v1/ui/diff`，scope `ui`。请求 `{before_frame_id, after_frame_id}`，`after_frame_id` 省略时现拍一帧。帧必须来自 `observe(tree=true)`：按 `frame_id` 缓存扁平树，容量 8 帧、TTL 60 秒、LRU；未知或过期返回 404 `frame_not_found`。节点用前序结构路径（如 `0/3/2`）配对并比较属性，返回 `added[]`、`removed[]`、`changed[]{node_id,before,after}`、`counts`、`truncated`，变更上限 500 条。语义是结构性差异，不承诺最小编辑距离；节点插入导致后续路径平移时如实报多个 changed。`observe` 的树改为扁平 `{count,nodes:[primitiveNode...]}`，与 `/v1/ui/tree` 的节点字段一致（`node_id`/`parent_node_id`），不再返回无 node_id 的原始嵌套树。
- 同时修正的既有缺口：`/v1/ui/list` 补 `page_fingerprint`（D29 原文要求，当前缺失）；文档中已不存在的 `tests/fakedev/` 与 `installer-test.sh` 描述改为现实。
- 非目标：不做持久化 metrics/时序数据库；不做跨设备地图同步；不做最小编辑距离或语义树匹配；不新增 scope。
- 验收：
  - 单测：metrics 并发、分位、白名单外归 `other`、字段固定；指纹同树相同/仅 `selected` 不同则不同；地图 YAML 往返保留新字段、漂移正反两向；diff 同树零变化、增删改各一条、上限截断、缓存 TTL/容量、未知帧 404。
  - 真机：metrics 调用前后计数增长且响应 < 64 KiB；`map where` 对真实当前页返回 `drift=false`，篡改指纹后出现警告；同页两次 `observe` diff 为空，切屏后非空。
  - 回归：`chopin acceptance` 与 `chopin journey` 全绿；静态门禁 0 失败。
  - 首次实测（2026-11-21）：acceptance PASS 41 / FAIL 0 / SKIP 0；journey PASS 21 / FAIL 0；metrics 22 请求、响应 1.3KB；diff 同页 0 变化、切屏 added 49/removed 169、过期 404；地图漂移 live/drift 两例。

#### D34：撤销通话能力（2026-11-22）

- 背景：通话能力（状态/挂断、录音自检、通话录音、通话上行注入）由 S9/S14 实现，依赖 APK 的 `InCallService` 与一组特权权限。用户决定移除打电话功能：远控主线不再包含通话。
- 决策：
  1. 删除 `/v1/call`、`/v1/call/end`、`/v1/call/audio`、CLI `call`、`InCallService`/`InCallBridge`/`CallAudioRecorder`/`CallAudioUploader`，以及通话权限（`CAPTURE_AUDIO_OUTPUT`、`CAPTURE_MEDIA_OUTPUT`、`MODIFY_PHONE_STATE`、`READ_PRECISE_PHONE_STATE`、`READ_PHONE_STATE`、`CALL_PHONE`、`ANSWER_PHONE_CALLS`、`MANAGE_OWN_CALLS`、`RECORD_AUDIO`）。
  2. `speak` 的 `--incall` 上行注入与 `incall_music_uplink` 48k 立体声分支删除；普通音频（状态、话筒静音、扬声器路由、采集 RMS、PCM 播放）保留。
  3. APK 仍由模块安装为 priv-app（D27 装机契约不变）；`privapp-permissions-chopin-pilot.xml` 保留文件、移除通话权限条目。
  4. 短信、联系人、SIM PIN 解锁、日常设置不属通话，保留。
- 原因：用户决策；通话不是远控主线的必需能力，且它是 APK 特权安装的唯一权限来源。
- 非目标：不删除普通音频；不删除 APK 的 HUD/IME/a11y；不改 priv-app 安装形态；不抹掉历史验收记录。
- 验收：`/v1/call*` 返回 404、CLI `call` 不存在、`/doc` 无三条 path、APK manifest 无 InCallService 与通话权限、`dumpsys package` 无通话组件、普通 `audio speak` 可用；acceptance 与 journey 全绿。

#### D35：会话后台回收（2026-11-22）

- 背景：D12 与 §9.6 写了 `idle_sec` 超时由 reaper 回收，但实现里 `Reap` 只由 `POST /v1/guard/reap` 触发，没有任何后台调用；会话空闲不会自己释放。旧 hub 的 phone-hub 用 keeper 按心跳闲置自动收工，chopin 缺这一层。`session.linger_sec` 在代码中没有任何使用点。
- 决策：
  1. 守护启动一个后台 reaper，每 `session.reap_sec`（默认 60 秒）调用一次 `Sessions.Reap()`；`reap_sec=0` 明确关闭定时回收。
  2. 回收沿用现有语义：`LastSeen` 超过 `idle_sec` 的会话被释放，agent 进入 roster ban `roster_ban_sec`；每次实际释放后发布 sessions 状态并写审计（释放数量）。
  3. 本轮**不删除** `session.linger_sec`：配置加载使用 `DisallowUnknownFields`，直接删字段会让已有设备的 `core.json` 解析失败、守护起不来。它仍是死配置，记为债务，待单独做配置迁移（或赋予真实语义）后再删。
  4. HUD 停止与静默断开仍立即 `ReleaseAll`；reaper 只处理“没人管但还占着”的会话。
- 非目标：不实现 phone-hub 的 hold/linger 常驻模式；不自动关屏、不还原设置（reaper 只释放会话）；不删除 `linger_sec`（升级兼容风险）；不改变 claim/heartbeat/release 合同。
- 验收：单测覆盖周期回收、取消、`reap_sec=0` 禁用；真机把 `idle_sec`/`reap_sec` 调短，claim 后不心跳，等待后会话消失且审计出现 reap 行，随后恢复配置；acceptance 与 journey 不回归。

#### D36：任务收尾与屏幕联动（2026-11-22）

- 背景：claim 会自动解锁；`release`、`Reap`、HUD stop/断连只释放会话，不锁屏；异常判定用 `idle_sec=900s`；IME 只在正常 release 恢复；`keep-awake` 不随会话结束释放。用户要求：不进行任务时不常亮、任务结束收尾锁屏、agent 异常终止自动释放会话并锁屏。
- 决策：
  1. 统一收尾 `finishTask(reason)`：最后一个写会话消失且 handover 未激活时，释放 keep-awake、恢复默认 IME、锁屏（`KEYCODE_SLEEP`），并写审计；handover 期间人在用手机，不锁屏。
  2. 挂到正常 `release`、手动 `reap`、后台 `reap`、HUD stop、HUD 静默断开。
  3. 异常判定新增 `session.stale_sec`（默认 180 = 3 × `heartbeat_sec`）；`Reap` 用 `min(会话 idle_sec, stale_sec)`，`stale_sec=0` 退回只按 `idle_sec`。
  4. 有活跃写会话且 agent 暂时无动作时，不主动息屏（任务进行中）；无会话时由收尾锁屏保证不常亮。
  5. `keep-awake` 在收尾时释放，异常终止不会留下永久唤醒锁。
- 非目标：不实现 phone-hub 的 hold/linger 常驻模式；不在会话进行中息屏；不改 claim 自动解锁；不删除 `linger_sec`。
- 验收：单测覆盖 stale 判定；真机 release 后 `screen OFF + keyguard true` 且审计有 task finished；claim 后不心跳，短 stale 配置下会话消失并锁屏；HUD stop 锁屏；acceptance 与 journey 不回归。

#### D37：会话活动信号与僵尸回收（2026-11-22）

- 背景：心跳只证明使用端进程活着，不证明任务在推进。使用端继续心跳而 agent 已失效时，会话永不过期、屏幕常亮。反向：CLI 的请求不发送 `X-Chopin-Session`，也没有 heartbeat 命令，D36 的 `stale_sec` 会误杀仍在操作的 CLI agent。
- 决策：
  1. **活动信号**：任何带有效 `X-Chopin-Session` 的业务请求都更新 `LastSeen` 与 `LastAction`；`/v1/sessions/{id}/heartbeat` 只更新 `LastSeen`。活动判定在 API token 与 scope 校验通过之后，无效会话 token 不续期、也不改变端点行为。
  2. **僵尸回收**：新增 `session.zombie_sec`（默认 300 秒，0 关闭）。后台每 `reap_sec` 检查“心跳仍新（普通 stale 未过期）但 `LastAction` 超过阈值”的会话，释放且不 ban，随后走 `finishTask` 锁屏并写审计。心跳也过期的会话仍由普通 `Reap` 处理。
  3. **CLI 本地会话**：`chopin claim` 把 session id/token 存到 `~/.chopin/session.json`（0600）；后续 CLI 命令自动带 `X-Chopin-Session`；`chopin release` 删除；新增 `chopin heartbeat` 读取本地会话续期。
  4. 本地会话是单用户单设备状态；多 agent 并发必须各自用 HTTP 客户端并显式带 header，不共享该文件。
- 非目标：不实现客户端执行器生命周期托管（使用端仍应在 agent 子进程退出时 release）；不区分只读/写业务请求（都算活动）；不 ban 僵尸 agent；不改变 claim 自动解锁。
- 验收：单测 Touch/心跳/僵尸判定/阈值 0；真机 claim 后只心跳不操作，短 zombie 配置下会话被回收并锁屏；claim 后持续业务请求不被回收；CLI 后续命令带 session；acceptance 与 journey 不回归。

### 6.2 仍然有效的早期决策

以下决策承载的是真机证据与 ROM 事实，不是可选项。与远控无关的 ROM 移植/镜像决策已移出本工作区。

#### D6：纯 KSU 模块，不刷镜像

- 决策：定制只走 `chopin-core` KSU 模块，不刷镜像、不改 stock system。
- 原因：用户要求模块优先；不刷机、回退简单。
- 回退：停用/卸载模块 + 重启；stock system 不动。

#### D7：默认不 hook

- 决策：不写通用插件 SDK、默认不写直接 hook；只在组件/设置/资源/框架都无解时使用，且单独开关。
- 原因：hook 与防检测目标冲突，只作兜底。

#### D8：守护内部组件分层

- 决策：守护内部按组件分层：`daemon`（Go：API/token/会话/锁/审计/策略/WebRTC/编排）、`dex-helper`（root `app_process`：framework binder、实时 a11y、通知、SMS、定位、捕获、H.264、OCR、音频）、`native`（OCR/编码/图像，优先运行时加载 ROM 内 MIUI 引擎，不复制分发）、`scrcpy-server`（虚拟屏 H.264 捕获）。
- 原因：纯 App/Shizuku 管不了 framework 行为；root daemon + framework binder 能在不装特权 App 的前提下覆盖通知/无障碍/SMS/定位。
- 验收：默认 profile 不依赖 LSPosed/Vector；模块可独立卸载；每项有实机证据；不出现 phoneagent/Portal 运行时依赖。

#### D9：截屏安全层走 systemless overlay 或按需捕获

- 决策：不在默认 profile 中全局清除 `FLAG_SECURE`；优先按需安全捕获，其次 framework jar systemless overlay。
- 原因：全局清安全层影响所有应用敏感内容保护；且与防检测目标冲突。
- 回退：stock system 不动；停用/卸载模块 + 重启即恢复。

#### D10：不重签、不修改系统 APK

- 决策：不重签 MIUI 系统 APK；行为差异用组件禁用/启用、settings、模块、framework jar systemless overlay 处理。
- 边界：组件禁用必须可逆；如确需新增系统级行为修正，必须作为独立设计提案，不在本工作区默认范围内。

#### D11 / D15 / D17（远控能力与组件合同）

- **统一 API**：一个 HTTP API（`/v1`）、一个会话/锁 owner；agent 只访问守护。
- **root helper**：root（uid 0）在 `ActivityManager.checkComponentPermission` 直接 granted；通知监听、实时无障碍、SMS、定位均可通过 root binder 实现，不依赖特权 App。
- **OCR**：优先运行时加载 ROM 内 MIUI OCR（`MiuiScanner` 的 `OCREngine` 或 `MIUIContentExtension_S` 的 `OcrNative`）；不复制分发 MIUI 专有代码。
- **IME 合同**（见 §9.3）：IME 在 APK 内，service 与 provider authority 是两个不同的字符串，必须分开配置。
- **HUD 合同**（见 §9.4）：HUD 在 APK 内，`TYPE_ACCESSIBILITY_OVERLAY`，只做状态胶囊 + 停止；它是安全提示不是审批。
- **不降级**：任何「做不了」必须给出 ROM 类/方法/权限/运行时证据；不得用轮询/空实现/假成功替代（D25）。

#### D12：TLS + token 鉴权、per-display 会话与锁

- 决策：服务端自签 TLS 证书，客户端 pin；不做客户端证书；API token + session token；`/doc` 公开只读。
- 会话：每个 display 一个写会话；最多 2 个（主屏 + 1 虚拟屏）；同一 owner 可同时持两屏；同一 display 单写。
- 全局配置（settings/屏幕/旋转/亮度/超时/飞行/Wi-Fi/安装卸载/清数据/重启/组件/权限/AppOps）只有在没有其他活跃会话时允许；否则 `exclusive_required`，不排队、不抢占、不冻结。
- 锁语义：claim/release/heartbeat、900s 闲置、90s 余温、roster/ban、guard、keeper、崩溃恢复、人在用判定、标准态还原；整机单实例改为 per-display 单写 + 全局配置独占 gate。
- 中文输入：可编辑节点优先 a11y `ACTION_SET_TEXT`，按 display 独立并行；控件不接受 a11y（微信自绘输入框）时切 IME `InputConnection` 路径。
- 微信节点树：**放行规则是「按已绑定服务的组件名做白名单」**（不是加密、不是 FLAG_SECURE）。同一时刻单变量实测：

| 启用/绑定的服务 | 微信 `tree` |
|---|---|
| 无 / 只有 `.hud.RemoteA11yService` | **1 节点 0 文字**（bounds 全 0） |
| 加一个**真的绑定上**的、名为 `com.google.android.accessibility.selecttospeak.SelectToSpeakService` 的服务 | **421 节点 82 文字**（会话列表、时间戳、草稿、聊天页消息全可读） |
| 只把该组件串写进 `enabled_accessibility_services`（我们包内没有这个类，绑不上） | **1 节点** |
| 系统自带的 `com.google.android.marvin.talkback/...SelectToSpeakService` | 421 节点 |
| 系统自带 TalkBack 主服务 `com.google.android.marvin.talkback/.TalkBackService` | 421 节点 |

结论：
1. **看的是「真的绑上了」，不是设置串**——所以必须在自己 APK 里声明这个组件并让它绑定，光写 settings 无效；
2. 白名单不是精确匹配某一个类名（TalkBack 主服务也放行），也对包的 uid/签名无感（我们自己包里放同名类一样放行），所以比对的是**组件串的子串**。

因此落地方式是**两个服务分工**：
- `.hud.RemoteA11yService`：真正干活的（读树、手势、HUD），**不伪装**；
- `com.google.android.accessibility.selecttospeak.SelectToSpeakService`：**零功能**兼容服务，`canRetrieveWindowContent=false`、不发手势、事件回调空实现，唯一作用是让它的组件名出现在已绑定列表里。

这样「伪装的名字」和「实际能力」落在不同服务上，能力不被扩大。守护把两者都当作要维持的目标（`keepalive.a11y` + `app.a11y_compat`），状态里分 `component` 与 `compat` 两个字段——`OK` 且 `compat` 未绑定会报 `DEGRADED: compat service not bound`，因为那正是「微信会读到空屏」的现场。

- 保留：审计 30 天；截图/录像 7 天 + 配额。
- Token scope：`read,ui,input,apps,files,settings,unlock,sms,location,notifications,webrtc,triggers,display,admin,hud,device`；HUD token 只允许 loopback 且只有 `hud` scope；WebUI 使用最小 scope（`read,ui,input,display,webrtc`）。

#### D13：危险操作由 agent 二次确认

- 适用范围：`apps/uninstall`、`apps/clearData`、`device/reboot`。
- 第一次调用返回 `confirmation_required` + `confirmation_id` + 摘要 + 过期时间；重新确认需带 `confirmation_id`、`confirmed_by_agent=true`、`intent=user_initiated|agent_inferred`、`reason`。
- 一次性、TTL 5 分钟；不做人工审批队列。

#### D14：解锁（屏幕图案 + SIM PIN，本地凭据）

- 两条链路：**屏幕图案锁** + **SIM 卡 PIN 锁**；不支持屏幕数字 PIN/密码/指纹/人脸，遇到返回 `unsupported_lock_type`，不盲点。
- 凭据：`config/unlock-secret`（图案）与 `config/sim-pin`（可选，`unlock.sim_pin_file` 为空即关闭该链路），0600 root 拥有；运行时值不进 argv/日志/API/审计（入库例外见 D26）。SIM PIN 剩余尝试 ≤1 时拒绝（花掉最后一次会被 PUK 锁死）。
- 触发：会话 claim 时自动解锁；`POST /v1/device/unlock-local`（app token + loopback）；`POST /v1/device/sim-unlock`（SIM 提示单独处理，不伪装成图案解锁）；开机自动解锁（`unlock.auto_on_boot`）。
- 真机三条硬约束（必须实现，不得退化为注释）：
  1. **唤醒判据用 `dumpsys display` 的 `mScreenState`**，不是 `mWakefulness`。MIUI 会停在 `mScreenState=OFF` 而 `mWakefulness=Awake`，此时 UI dump 只有 scrim 节点 → 会误报 `pattern_view_not_found`。
  2. **上滑揭示最多 3 次**：MIUI 对同一次上滑有三种回应（展开通知栏 / 亮出 bouncer / 手势中途熄屏只剩 scrim）；每轮前确认面板还亮着；仍失败才 `wm dismiss-keyguard`。
  3. **先读凭据再动屏幕**：缺凭据立刻 `credential_missing`，一个手势都不画（否则把手机停在半开键盘锁界面）。
- 顺序：唤醒 → SIM PIN（若需；剩余尝试 ≤1 拒绝）→ 屏幕图案（若 `isKeyguardShowing`）→ 回读 `isKeyguardShowing=false`。
- 安全：单次尝试、失败不重试、30 秒冷却、审计方法/成功/失败（不含凭据）。
- 验收：熄屏+锁屏 → 自动解锁 → `RUNNING_UNLOCKED`；无凭据干净失败；图案错误只试 1 次。

#### D16：内嵌 WebUI

- 决策：`GET /ui` 由守护直接提供静态单页（embed），复用 TLS/token/`/v1`/WebRTC/input；不新增 Web 后端、不引入前端构建链、不做管理后台。
- 鉴权：登录粘贴 API token 或配对材料；token 只存 `sessionStorage`，不写 localStorage、不放 URL、不进日志；写请求带 `Authorization: Bearer` + `X-Chopin-Session`。
- 会话：WebUI 是普通客户端；display 空闲时 claim 写会话，agent 已持有时只读看屏、输入禁用、不抢占；心跳 60s；业务请求自动续期；页面关闭 best-effort release；守护每 `session.reap_sec` 回收超过 `stale_sec`（默认 180s）未心跳或超过 `zombie_sec`（默认 300s）无业务操作的会话；最后一个写会话结束时走任务收尾锁屏。
- 能力：主屏/现有虚拟屏 WebRTC 看屏；tap/swipe/longpress/key/text；会话 claim/release/状态；不做应用/文件/设置/危险操作管理，不做录制/音频/多设备。
- 流：WebRTC H.264 为主；浏览器不支持/ICE 失败时显式切 `/v1/ui/frame` 快照模式（约 2fps）并在 UI 标注，且标注本身就是“降级”——因此 WebRTC 路径是本轮验收项，快照只用于排障。
- 安全：自签 TLS；CSP `default-src 'self'`、无 CDN/内联脚本；服务端数据只用 `textContent` 渲染；`X-Frame-Options: DENY`；`X-Chopin-Client: webui` 进审计。

### 6.3 已移出本工作区的决策

以下内容不再属于当前设计范围，原文保留在 Git 历史中：

- 官方底包/反编译基线、XMAPort 移植、镜像打包、分区操作；
- `chopin-compat` 的 ROM 行为修正（10s 权限拦截页、旋转、Uperf、watchdog）与 `miui14-agent-tune` 的 Portal 保活；
- 任何「第二套 a11y 保活 / 第二套状态真源 / 第二个 KSU 模块」的形态。

---

## 7. 配置合同（`config/core.json`）

单目标、声明式。一个字段只承载一个语义。

```json
{
  "schema": 1,
  "listen": "0.0.0.0:8443",
  "data_dir": "/data/adb/chopin-core",
  "app": {
    "package": "com.chopin.pilot",
    "a11y": "com.chopin.pilot/.hud.RemoteA11yService",
    "a11y_compat": "com.chopin.pilot/com.google.android.accessibility.selecttospeak.SelectToSpeakService",
    "ime_service": "com.chopin.pilot/.ime.RemoteImeService",
    "ime_provider": "com.chopin.pilot.ime",
    "hud_package": "com.chopin.pilot"
  },
  "keepalive": {
    "interval_sec": 15,
    "a11y": { "enabled": true, "settle_max_sec": 6, "skip_when_foreground_settings": true },
    "bg": { "enabled": true, "autostart": "allow", "bgcontrol": "noRestrict", "doze": true },
    "perms": ["idle", "runtime"]
  },
  "unlock": { "credential_file": "config/unlock-secret", "sim_pin_file": "config/sim-pin", "attempts": 1, "cooldown_sec": 30, "auto_on_boot": true },
  "session": { "idle_sec": 900, "stale_sec": 180, "zombie_sec": 300, "reap_sec": 60, "heartbeat_sec": 60, "guard_stale_sec": 600, "roster_ban_sec": 900, "max_write_sessions": 2 },
  "helper": { "socket": "@chopin-core-helper", "dex": "lib/helper.jar" },
  "scrcpy": { "jar": "lib/scrcpy-server.jar", "max_size": 1280, "bit_rate": 4000000, "max_fps": 30, "stream_max_viewers": 3 },
  "audit": { "max_bytes": 1048576, "keep_days": 30 },
  "limits": { "max_body_bytes": 1048576, "request_timeout_sec": 1800 },
  "hud": { "heartbeat_sec": 15, "capture_settle_ms": 120, "idle_hide_sec": 25 },
  "human": { "enabled": true, "gap_min_ms": 700, "gap_max_ms": 1600, "tap_inset": 6, "jitter_px": 6, "wait_min_pct": 80, "wait_max_pct": 135 },
  "challenge": { "enabled": true, "slider_attempts": 2, "cooldown_sec": 600, "risk_days": 7 }
}
```

- 字段校验在启动时做：未知字段报错（不静默忽略）、数值越界回落到文档声明的最小值并记 WARN（`interval_sec` 最小 10）、`ime_service`/`ime_provider`/`a11y`/`a11y_compat` 必须能被设备上真实存在解析。
- `human.enabled=false` 时注入完全确定性（不给抖动、不加间隔），用于基准测试与 A/B 对照；默认 true。
- `challenge.cooldown_sec` 是命中验证码后不再自动操作该包的时长；`risk_days` 是 `state/risk.json` 记录衰减天数。
- `data_dir` 是模块固定属性，不放进配置的可改项（见 `modules/chopin-core/README.md`）。
- 运行时配置在 `/data/adb/chopin-core/config/core.json`；升级不覆盖用户改动。

---

## 8. HTTP API 合同

- 全部能力在 `/v1` 下；统一错误信封 `{"error":{"code","message","detail"}}`。
- 通用响应头：`X-Chopin-Schema: 1`、`X-Chopin-Request-Id`。
- 鉴权：`Authorization: Bearer <api-token>`；写操作额外 `X-Chopin-Session: <session-token>`（会话绑定的 display）。带有效 session 的业务请求更新会话活动时间；`/v1/sessions/{id}/heartbeat` 只续心跳，不算业务活动。
- scope 校验：每个端点声明所需 scope（见 §6.2 D12 的 scope 集合）；缺 scope 返回 `403 forbidden_scope`。
- 端点按 §5 能力矩阵分组实现；`GET /doc` 返回 OpenAPI 3.1，由代码里的路由表生成，不手写维护。**路由表是精确清单**。
- 上界：请求体 ≤ 1 MiB；`/v1/ui/*` 与 `/v1/sessions` 超时按 `limits.request_timeout_sec`；SSE 连接有背压与断开检测；上传分片有上限。
- 危险操作按 D13 二次确认。
- `/v1/health`、`/doc`、`/doc/skill`、`/doc/skill.zip` 公开只读（TLS 下）；其余全部需要 token。
- `/v1/ui/frame` 返回 `image/jpeg` 字节（不是 JSON），错误仍用统一错误信封；`/v1/ui/screenshot` 返回 JSON 元数据。
- `/v1/ui/observe` 一次截帧，按 `tree`/`ocr`/`pixels`/`frame` 选择返回同一帧的结果，并附 `frame_id` 与 `timings`；树节点带 D29 的三态属性与 `node_id`/`parent_id`。
- `/v1/ui/act` 返回 `dispatched`/`channel`/`clicked_bounds`/`observed`/`verified`/`failure`；不自动重试，`unknown` 不冒充成功。
- `/v1/ui/list`、`/v1/ui/options`、`/v1/ui/readback` 返回结构化项/选项/回读结果；`/v1/ui/page` 返回页面指纹、窗口/弹层与键盘状态；`/v1/ui/wait_stable` 返回稳定判定与原因。
- `/v1/hud/ack` 只接受 loopback 与 `hud` scope，用于 HUD 回执 capture/pass-through 指令（D28）。
- 注入类端点（`/v1/input/*`、`/v1/ui/act`、`/v1/ui/chain`、`/v1/ui/find` 的点击）响应增加 `point`（实际落点；a11y 通道为 null）、`seed`、`dispatch_ms`、`paced_ms`、`interval_ms`（距上一次注入完成的实际间隔）；请求可带 `seed` 复现（D30）。
- `POST /v1/ui/challenge` 返回 `{kind, evidence[], package, frame_id}`；`POST /v1/ui/challenge/solve` 只处理 `slider`，返回 `{solved, attempts, dx, kind}` 或 `challenge_unresolved` / `challenge_needs_human`。
- `GET /v1/challenge/risk` 返回包级验证页记录（`count`/`last`/`until`/`cooling`/`describe`），供配方在执行前判断。
- `POST /v1/device/handover`、`POST /v1/device/handover/return`、`GET /v1/device/handover` 管理人工接管状态；接管期间注入端点统一返回 409 `handed_over`（D30）。

---

## 9. 客户端合同

### 9.1 状态（唯一真源）

`state/status.json`（以及发给 APK 的副本），schema 固定：

```json
{
  "schema": 1,
  "updated_at": "2026-11-16T19:00:00+08:00",
  "daemon": { "pid": 17161, "version": "3.0.0", "uptime_sec": 120, "state": "running" },
  "app": {
    "package": "com.chopin.pilot",
    "installed": true,
    "version_name": "3.0.0",
    "a11y": { "state": "OK", "detail": "bound", "component": "com.chopin.pilot/com.chopin.pilot.hud.RemoteA11yService", "compat": "com.chopin.pilot/com.google.android.accessibility.selecttospeak.SelectToSpeakService" },
    "bg": { "autostart": "allow", "bgcontrol": "noRestrict", "doze": true, "ok": true },
    "perms": { "idle": "OK", "runtime": "OK" }
  },
  "unlock": { "credential": "present", "screen_locked": false, "last_attempt": "ok" },
  "sessions": { "active": 1, "max": 2, "roster": {} },
  "errors": []
}
```

- 状态值域：`OK`（读回等于期望）、`DEGRADED`（写入成功但运行时未生效）、`MISSING`（组件/包不存在）、`PENDING`（本轮跳过，下轮重试）、`DENIED`（系统拒绝，带 reason）。
- 状态必须来自真实读取（`dumpsys`/`settings get`/`appops get`/`pm list`），禁止固定值。
- 写入原子替换（`tmp` + `rename`），读方永远看到完整 JSON。
- APK 只读 `state/app/status.json`；PC 用 `/v1/status`；字段完全一致，`schema` 不匹配时双方显式报错。

### 9.2 APK（`apps/pilot`）

- 组成：`MainActivity`（状态页 + 解锁凭据录入）、HUD 无障碍服务、IME 服务、前台服务（订阅状态并保活 UI）。
- **零 root**：不申请 KernelSU 授权、不执行 `su`、不拉起任何进程、不写 allowlist、不写 `enabled_accessibility_services`。
- 状态页数据来自 `state/app/status.json`（守护 chown 到 app uid）；发布失败时显示「守护未发布状态」并给出守护是否在跑的判断依据（读 pid 文件失败即未跑）。
- 凭据录入：值经 stdin 传给 `chopin-core ctl secret set-unlock`（ctl 由模块安装到 `/data/adb/ksu/bin`，通过 `su` 调用——这是 APK **唯一**的特权动作，且只写凭据不读回值）。

  > 说明：这不违反 D21。D21 取消的是「给 APK 授予 root 以运行引擎」。凭据录入必须经 `su` 才能写到 root-only 路径；若要求 APK 完全不用 `su`，则凭据只能由 adb 录入，锁屏下不可达。当前选择：允许这一条 `su -c 'chopin-core ctl secret set-unlock'`，并且 ctl 只接受 `set-unlock`/`clear-unlock`/`status` 三个子命令用于此路径。

- 前台服务只订阅状态与维持通知，不再守护引擎进程。
- 包名 `com.chopin.pilot`；minSdk 29 / targetSdk 33；签名 keystore 在 `apps/pilot/build/keystore/`（ignored，升级必须复用）。

### 9.3 IME 合同

- service：`com.chopin.pilot/.ime.RemoteImeService`；控制 provider authority：`com.chopin.pilot.ime`。**两者不是同一个字符串，必须分开配置**（混用会导致文字注入永远失败，已发生过的 bug）。
- provider `call` 方法：`input`（`arg` = Base64 UTF-8 文本，`extras.clear`）、`clear`、`key`（Enter/Back 等）。
- 返回值：`result ∈ VERIFIED|ACCEPTED_UNVERIFIED|REJECTED|COMMIT_OUTCOME_UNKNOWN|INPUT_SESSION_CHANGED|NO_INPUT_CONNECTION`。
- provider 只接受调用 UID 0、2000 或自身 UID；其他抛 `SecurityException`。不记录输入文本，只记录长度/结果。
- 输入法生命周期：守护在首次 IME 回退前保存当前默认 IME（写 `state/ime.previous`，不只存内存），`ime enable` + `ime set` 到本 IME，轮询 `dumpsys input_method` 直到 `mCurMethodId`/`mCurId` 指向它；**最后一个写会话 release 时**与守护退出时恢复原默认 IME。恢复失败必须记录并在状态里暴露。
- 恢复的用户优先规则：若当前默认既不是我们的、也不是记录下来的那个，说明用户自己换过输入法，此时**不动它**，只清掉记录。
- 输入视图高度 48dp 的 `TextView`；0/1dp 空 View 在 MIUI 上会让 insets 动画把目标应用内容截成空白。
- 并发：Android 13 一个用户只有一个默认 IME 会话 → 守护用全局租约串行化，冲突返回 `text_input_busy`；a11y `ACTION_SET_TEXT` 不受限、仍 per-display 并行。
- 边界：IME 只解决「聚焦输入框的文本提交」，不负责点击/聚焦；无 InputConnection 返回 `text_input_unavailable`，不假装成功。

### 9.4 HUD 合同

- HUD 是 APK 内的无障碍服务，`TYPE_ACCESSIBILITY_OVERLAY`（无需 `SYSTEM_ALERT_WINDOW`），三档形态（胶囊 / 卡片 / 小球）+ 屏幕边缘色带。
- **胶囊要说出「正在做什么」，不是「控制中」。** 每次注入都经 `hud.NoteAction` 记账，文案由守护给（`点「通讯录」` / `输入 7 字` / `返回` / `滑动 a,b → c,d`）。**输入内容只报字数**。
- **暂停与停止必须真的拦住动作。** 所有注入端点（tap/swipe/longpress/key/global/text/chain/find+click）统一走 `refuseIfPaused`，返回 409 而不是静默放行。
- **叠加层的可见性不能依赖客户端 claim。** 任何动作都让叠加层可见，并加空闲自动收起（`hud.idle_hide_sec`，默认 25s）；有活跃会话时由会话持有可见性，空闲计时器不碰它。
- **每个新动作都要把胶囊唤回来。** 折叠计时器只在任务开始时 `arm` 一次，之后状态更新只重绘那一行文字——于是第一次状态 3.5s 后收成半藏小球，整段任务剩下九分半都只露半个球。新增 `FoldClock.activity()`：有新动作就置为胶囊可见并重启计时；**手动收起的球不动**。
- **心跳只表示"还活着"。** `Paused` 只在人点「继续」时清，`Stopped` 只在下一次 claim 时清（`Attach`）。
- 守护 → HUD：`GET /v1/hud/events`（SSE）；HUD → 守护：`POST /v1/hud/stop`。HUD 只持 `hud` scope 内部 token，只允许 loopback。`GET /v1/hud` 接受 `read` **或** `hud`：普通 PC token 用于 WebUI/journey 读设备状态，HUD token 用同一端点复核自己收到的 SSE 状态；两者都受 loopback 限制。
- HUD 供料：守护启动时写 `app/files/hud.json`（`server_url` 为 loopback、`server_cert_sha256`、`token`（`hud` scope、LocalOnly）、`socket_secret`），chown 给 APK uid。token 仍可用就复用，不每次重启换。
- 写会话 claim 前要求 HUD ready；HUD 断连超过 `hud.heartbeat_sec`（默认 15s）自动释放所有写会话（`ReleaseAll("hud_disconnected")`）。
- 截图/注入期间 HUD 必须隐藏（截图不带 HUD、注入不命中停止键）。指令走 **SSE 状态帧**的 `capture_hidden` / `pass_through` 字段。D28 起指令带 `request_id`，HUD 应用后 `POST /v1/hud/ack`；守护收到 ACK 立即继续，超时（`hud.capture_settle_ms`，默认 120ms）才按固定等待继续。没有 HUD 连接时不等。
- 叠加层没连上时不算失败：没有 HUD 就没有要隐藏的东西，截图与注入照常进行，状态里 `connected=false` 如实反映。
- HUD 是安全提示，不是审批；停止会释放所有远程写会话、还原屏幕、审计，并走任务收尾（恢复 IME、锁屏；handover 期间不锁）。

### 9.5 PC CLI（`apps/pc-cli`）

- 安装：解压即用；`chopin pair --serial <adb-serial>` 完成配对，之后所有子命令直接可用。
- 子命令覆盖 §5 全部能力组，与端点一一对应；`chopin doc` 导出 OpenAPI；`chopin acceptance` / `chopin journey` 跑真机验收并输出证据包。
- token 存 `~/.chopin/config.json`（0600）；证书指纹在配对时 pin 并在每次请求校验。
- 不做业务判断，不做自动重试掩盖失败。

### 9.6 WebUI 与手机状态页

- WebUI 与手机状态页是同一个产品的两个视图：顶部 rail 显示守护（状态 + pid）、无障碍、后台放行、会话数、运行时长；视频下一行显示 agent 当前动作，与手机叠加层同一句。
- 颜色只承担状态（正常/降级/危险），调色板是同一套雾蓝中性色；等宽字体只给测量值。
- 手机页：四格读数 + 状态 chip；`UiKit.java` 是这一页唯一的样式来源；亮暗从系统 `uiMode` 读；`render()` 每 5 秒只改文字和颜色，不重建视图树。
- 手机页必须显示真实 WebUI 入口：`https://<wlan0 地址>:8443/ui/`，地址从网卡枚举取，不用占位符。
- `ScrollView` 子 View 必须显式 `WRAP_CONTENT` 且 `setFillViewport(false)`；否则会被测成视口高度，整页压扁。
- 无障碍硬指标：文本对比度 ≥4.5:1，触摸目标 ≥36px，`prefers-reduced-motion` 与 `prefers-color-scheme` 有真守卫；WebUI 的 id 契约由 `check-contracts.sh` 锁定。

### 9.7 音频与日常设置

- 音频走 helper（root + 系统 Context）；普通应用拿不到 `CAPTURE_AUDIO_OUTPUT`，`pm grant` 也救不了。
- `setMicrophoneMute` 的返回值不等于生效；必须读回比对，不一致返回 409 并说明原因。`listen` 的 `rms≈0` 返回 200 + `silent:true` + 原因，不返回 5xx。
- `speak` 收字节不收文本（本机无 TTS）；普通播放 16 kHz 单声道 s16le。
- 日常设置（飞行/Wi-Fi/蓝牙/定位/夜间/勿扰/音量/超时/亮度/自动旋转）必须走拥有它的服务并逐项读回；只写 Settings 数据库对飞行模式这类「镜像行」是谎。飞行模式会切断 Wi-Fi ADB 管理链路，文档必须写明。
- 打字首选 a11y `ACTION_SET_TEXT`（约 0.15s，不切默认输入法）；找不到焦点字段时先按坐标找可编辑节点，仍不行才回退 IME。节点点击优先于坐标点击（三层：节点自身 `ACTION_CLICK` → 含该点的最近可点祖先 → 坐标手势兜底）；屏幕外/负坐标节点拒绝点击；微信的行标题 bounds 可能反向，所有几何判断先 `Normalized()`。
- 滚动优先走节点 `ACTION_SCROLL_*`；没有可滚动节点才退回手势。

---

## 10. 边界、失败模式与硬约束

### 10.1 无障碍（唯一 owner 是守护）

1. **`settings put secure enabled_accessibility_services <同值>` 是 no-op**：AMS 的 `AccessibilityContentObserver` 只在原始字符串变化时重新求值。修复必须两步写：先写不含目标的值，再写完整值（实测 1–4s 内绑定）。
2. **`accessibility_enabled` 是派生值**：写它单独无效。
3. **被杀的服务进入 `Crashed services`**，约 180s 内不自动恢复；需要两步写法主动重绑。
4. **真实 bound 的判据**：`dumpsys activity services <pkg>` 中该组件 `hasBound=true` 且被 `system_server`（`system/1000`）绑定。`dumpsys accessibility` 的 `Bound services` 只有 label 无组件名，不能回答「组件 X 是否 bound」；settings 只是期望态。
5. **AMS 会持久化短形式**（`pkg/.Svc`）：比较前必须规范化为 `pkg/pkg.Svc`，否则每写一次就多一条重复。
6. **UiAutomation 会解绑全机第三方 a11y 服务**：helper 注册 `registerUiTestAutomationService` 必须带 `FLAG_DONT_SUPPRESS_ACCESSIBILITY_SERVICES(1)`，平台拒绝时才退回 0。
7. **前台是 Settings 时让行**，避免和用户手动操作打架。
8. **compat 服务必须真的绑定**：只写 settings 不够；`status.app.a11y.compat` 未绑定时报 `DEGRADED`。

### 10.2 框架 CLI 的 stdin

- 本模块 shell 调用 `pm`/`settings`/`cmd`/`am` 时一律 `< /dev/null`；读清单/状态文件用独立 fd（`3<file`），不要占用 stdin。
- 原因（真机证）：这些是由 `app_process` 启动的 Java 程序，进程启动时 fd0 会被带入 binder 事务；`system_server` 去 `read()` 一个 `/data/adb/**` 下的文件被 SELinux 拒绝（`avc: denied { read }`），报 `Failed transaction (2147483646)`、退出码 2。触发条件是文件标签（`adb_data_file`）而非内容。

### 10.3 锁屏与 CE

- 锁屏 + 非 directBootAware ⇒ 框架不解析任何非 DE 应用：`am start` 报 `Error type 3 / Activity class does not exist`，`resolve-activity` 报 `No activity found`。这不是 APK 坏。**不要**给 Activity 加 `directBootAware` 来"修"：它一启动就要读 CE 文件，锁屏下照样跑不起来。
- 锁屏造成的「不可验证」不得报成 FAIL：验收脚本先探 `RUNNING_LOCKED`，锁屏时把依赖解锁的项标 skip 并写明原因，其余真检查照跑。
- `/data/user_de/0/<pkg>` 存在 **不等于** 已解锁。

### 10.4 上界与资源

- 所有外部输入、队列、缓存、请求体、响应体、会话窗口、文件大小、重试、超时、后台 worker 都要有上界、背压、释放或降级策略；禁止无界增长与永久常驻的隐性运行面。
- 日志轮转：单文件上限 1 MiB，保留最近 500 行；审计保留 30 天。
- 截图/录像临时文件 7 天 + 配额。

### 10.5 安全硬约束

- 不 `pm uninstall` / `disable-user` 预装应用（唯一例外是本项目自己的 `com.chopin.*`）。
- 不刷写分区、不改 boot、不 relock BL。
- 凭据不进 argv / 日志 / API / 审计（入库例外见 D26）。
- 不提交 keystore / ADB key / keybox（公钥可提交）。
- a11y settings 键的唯一 owner 是守护；本工作区不再有第二个模块争用该键。

---

## 11. 实施切片与验收门禁

按能力纵切，每切片必须真机跑出结果才算完成（用户明确：真机功能验收为准）。

| 切片 | 内容 | 真机验收 |
|---|---|---|
| **S0** 设计收口 | 本文件 + `TASKS.md` + 工作区收敛 | 文档链接无失效；`git status` 干净 |
| **S1** 制品与宿主 | 模块 zip 含 daemon/helper/scrcpy/webui/APK；`service.sh` root 拉起守护；health/status/device、TLS、TokenStore、配对、status.json；APK 去 root | 刷 zip → 重启 → 守护 uid 0 `u:r:su:s0`；`chopin pair` → `health` → `status`；APK 状态页一致 |
| **S2** 解锁 + 输入 + 截图 | device/*、input/*、ui/screenshot、ui/frame | 熄屏锁屏 → 自动解锁；tap 命中；中文 text 注入；截图非全黑 |
| **S3** UI 感知与复合原语 | tree/ocr/find/wait/chain/color_probe/pixel_stats/panel_read/wait_result/events | 各端点返回真实结果；OCR bounds 正确；find 树优先、无则 OCR；events 连续 |
| **S4** 会话/并行/虚拟屏/流 | sessions/roster/guard/displays/scrcpy/WebRTC/stats | 主屏+虚拟屏并行写；第三人被拒；虚拟屏截图；WebRTC answer |
| **S5** 应用/设置/文件/信息 | apps/*、settings、props、components、watchdog、files、uploads、notifications、sms、location | 逐端点真机证据 |
| **S6** 触发器/WebUI/HUD/IME | triggers、`/ui`、`/doc`、hud/*、IME 端到端 | 触发器按类触发并留 run；真实浏览器看到画面；HUD pause 拒注入、stop 释放全部；IME 中文落到输入框 |
| **S7** 保活收口 | a11y/后台/权限组保活（Go 直调） | 清空 a11y 15s 恢复；杀进程恢复；UiAutomation 期间仍 bound；bg-ok=yes；重启自动就绪 |
| **S8** PC CLI + 收尾 | chopin 全子命令、`chopin doc`、`acceptance`、`journey`；删死代码；静态门禁收敛 | build → 刷 zip → 重启 → 逐组跑完能力矩阵，输出证据包 |
| **S9** 通话（2026-11-22 撤销，D34） | `/v1/call`、`/v1/call/end`、`/v1/call/audio`、InCallService、privileged 判定 | 历史：真拨真挂；录音能力诚实上报；特权安装后 `privileged=true` |
| **S10** 日常设置 | `/v1/settings/daily` 12 项走系统服务并读回 | 逐项真实值；飞行模式链路切断记录在案 |
| **S11** WebUI/手机页 | 五格 rail、活动行、同一套调色板、入口地址、图标 | 浏览器与手机页截图；对比度/触摸目标门禁；入口真实可读 |
| **S12** 音频 | `/v1/audio/*`、helper 音频桥、麦克风/扬声器/采集/播放 | 静音量级前后对比；路由读回；每种采集源如实区分「没权限/没声音」 |
| **S13** 收尾视觉 | APK 图标、入口地址、ScrollView 布局修复 | `aapt2` 图标存在；真机 OCR 全页文字可见 |
| **S14** 通话上行注入（2026-11-22 撤销，D34） | `incall_music_uplink` 48k 立体声、`--incall` 不碰扬声器 | 历史：真机 `getRoutedDevice()`；`speak --incall` 前后 speakerphone 不变 |
| **S16** 低延迟观测 | helper 快帧 + JPEG、`frame` 字节、`observe` 组合、最近帧复用、2fps 缓存、HUD ACK、事件驱动 wait | 真机 P95：frame ≤400ms、observe ≤700ms、tap ≤150ms；acceptance/journey 不回归 |
| **S17** 初始化与 skill | `chopin init`、`ctl secret set-sim`、动画基线还原、`/doc/skill` 与可安装包、`chopin skill` | 清空 tokens 后一条命令完成配对与凭据；skill 可下载并安装到 agent 目录 |
| **S18** 通用 UI 原语（D29 P0） | observe 帧/act 事务/list/options/readback/page/wait_stable；events 修复；CLI 一致性；`chopin run` 配方 | 真机：一次 observe/act/list/options 可用；wait_stable 替代 sleep；events 有真实事件；只读流程可复现 |
| **S19** 体验与可靠性（D29 P1） | 输入安全（回读/发送校验/键盘/危险按钮）、keep-awake、断点/进度、上下文裁剪、doctor/log | 长任务不熄屏、可恢复；发送前回读；大响应可裁剪 |
| **S26** 会话活动信号与僵尸回收（D37） | 带 session 的业务请求续期；`zombie_sec` 300s；CLI 本地会话自动带 + heartbeat | 单测活动/僵尸；真机只心跳不操作被回收锁屏、持续操作不被回收；acceptance/journey 不回归 |
| **S25** 任务收尾与屏幕联动（D36） | `finishTask`：release/reap/HUD stop/断连锁屏、IME/keep-awake 收尾；`stale_sec` 180s | 单测 stale 判定；真机 release 与异常回收后锁屏；acceptance/journey 不回归 |
| **S24** 会话后台回收（D35） | `session.reap_sec` 后台 reaper；`linger_sec` 保留为债务 | 单测周期/取消/0 禁用；真机短超时会话消失且审计有 reap 行 |
| **S20** 深度能力（D29 P2 / D33） | 增量树 diff、内存 metrics、页面指纹/版本与地图/配方漂移 | 真机：metrics 计数增长、diff 正反例、地图漂移告警；acceptance/journey 不回归 |
| **S21** 拟人操作与验证码分流（D30） | 种子化落点/节奏/限速；challenge 分类与滑块自解；risk 记录；handover 接管与注入拦截 | 同 seed 两次 run 完全一致；10 次 tap 间隔落在配置区间；滑块页可自解或结构化失败；短信验证码页 handover 后注入全 409 |
| **S22** App 地图层（D31） | 地图模型/识别/操作目录/相对宏/回放/爬取/bench；`map_*` CLI 与 recipe `map_nav` | 真实设备夹具单测；设置地图 ≥4 屏；NAV 精确落点 ≥2/3；无线电状态还原；`map nav` 与 `run` 可复现 |
| **S23** agent 引导入口（D32） | `/doc` 的 `x-chopin-guidance`（install/use 步骤）与 SKILL.md「从零安装」 | 单测锁定字段与阶段；真机 `/doc` 与 `/doc/skill` 内容一致 |

### 11.1 每切片完成标准

1. `go build ./...`、`go vet ./...`、`gofmt -l` 干净。
2. 该切片的 Go 单测通过（只测可离线验证的逻辑：解析器、状态机、会话规则）。
3. 该切片真机验收记录写入 `docs/TASKS.md`（命令 + 原始输出摘要 + 期望/实际）。
4. `git status --short` 干净，原子提交（`feat`/`fix`/`docs`/`refactor`/`test`/`chore`）。

### 11.2 静态门禁（收敛为 ≤ 60 项）

- `gofmt -l` / `go vet` / `go build`（守护 + PC CLI）。
- `sh -n` 全部模块 shell 与构建脚本。
- `check-contracts`：包名/组件/authority/路径/状态键在模块配置、APK manifest、PC CLI、WebUI 四处一致；`status.json` schema 键集三方一致。
- `verify-apk`：APK 能构建、签名一致、**无 `su`/root 依赖残留**（grep 断言）。
- `check-docs`：README 索引与真源一致；本工作区不含旧模块/研究资料；外部研究路径已记录。
- 每项自报计数，总数必须 ≤ 60；超出需说明理由。

### 11.3 设备验收

- `tools/scripts/device-acceptance.sh`：转发到 PC CLI，输出证据包 `out/device-acceptance-<ts>/`。
- `chopin acceptance --serial <adb>`：从 PC 侧跑能力矩阵并同样留证据。
- `chopin journey`：按使用流程跑 20 步，每步三条规则同时成立。
- 锁屏、未启动、权限缺失等环境态必须如实标 skip 并写原因，不得报 FAIL 也不得报 PASS。

---

## 12. 风险与缓解

| 风险 | 影响 | 缓解 |
|---|---|---|
| 全量能力工作量大 | 周期长、易半成品 | 按 §11 切片推进，每切片真机验收；未实现的能力不发布假实现 |
| APK 直连 root 的诱惑 | 破坏零 root 边界 | 当前 APK 零 root 是明确选择；能力都在守护，APK 不需要 |
| 凭据误入库 | 泄露 | 文件 gitignore + 构建前校验；不 force-add；运行时不进 argv/日志/API |
| UiAutomation 再次解绑 a11y | 保活永远修不好 | 契约断言 + 真机验收项（S7） |
| 锁屏/CE 行为与预期不符 | 无人值守失败 | D14 三条硬约束 + 锁屏态如实 skip |
| scrcpy-server 二进制不在 Git | 构建失败 | 构建脚本给出获取方式；外部研究目录保留参考 |
| 自签证书 pin 轮换 | PC 连不上 | 配对材料带指纹；`chopin pair --refresh-cert` 重新 pin |
| 守护被 MIUI 杀 | 能力中断 | flock 单实例 + oom_score_adj + 状态 heartbeat；不双开 |
| 两个模块 APK 签名/升级 | 升级失败 | 保留 keystore；签名不匹配先卸载再装 |
| root binder 被 SELinux/MIUI 挡 | 通知/a11y/SMS/定位不可用 | 真机 spike；按 ROM 找替代路径；失败阻塞，不降级 |
| ROM OCR 精度/加速不达标 | 视觉任务不可用 | spike；换另一个 ROM 引擎；失败阻塞 |
| IME 是全局通道 | IME-only 控件双屏输入串行 | a11y 路径 per-display 并行；IME 用全局租约返回 `text_input_busy` |
| 外部研究资料被误当产品真源 | 架构漂移 | 本文件 §2.2 明确边界；`check-docs` 断言工作区不含 `decompiled/`/`research/` |

---

## 13. 变更记录

| 日期 | 变更 | 证据/备注 |
|---|---|---|
| 2026-11-22 | **新增 D37：会话活动信号与僵尸回收** | 用户决策；心跳在但无操作 5 分钟释放并锁屏；CLI 本地会话自动带 |
| 2026-11-22 | **新增 D36：任务收尾锁屏与异常 180s 回收** | 用户决策；无任务不常亮、结束/异常收尾 |
| 2026-11-22 | **新增 D35：会话后台回收** | 用户决策；对齐 phone-hub keeper 的自动收工语义；`linger_sec` 因配置兼容保留 |
| 2026-11-22 | **撤销通话能力（D34）** | 用户决策；删除 `/v1/call*`、InCallService、通话录音与 `--incall` 上行注入；普通音频与 priv-app 装机形态保留 |
| 2026-11-21 | **撤销 D33 的剪贴板读取** | 用户决策；Android 10+ 后台限制使其不可用，且与 tree/OCR 感知重叠；端点和 CLI 一并删除，不保留 |
| 2026-11-21 | **新增 D33：S20 深度能力（内存 metrics、指纹/版本漂移、增量树 diff）** | 用户决策；S20 三项实现 |
| 2026-11-19 | **新增 D31：App 地图层（YAML 地图、文本重叠识别、操作目录、相对宏、校验式回放与爬取）** | 移植自 PhoneGo（MIT）；YAML/无 LLM/主屏默认允许 |
| 2026-11-19 | **新增 D32：agent 引导入口（`/doc` 的 `x-chopin-guidance` 与 SKILL.md 从零安装）** | 用户决策；S23 |
| 2026-11-19 | **新增 D30：拟人操作（种子化落点/节奏/限速）、验证码分流（滑块自解 / 短信邮箱人工接管）、risk 记录与 handover** | 用户决策；PhoneGo 源码与实测文档（`/root/chopin-research/research/phonego`） |
| 2026-11-18 | **新增 D29：agent 体验契约（七个通用 UI 原语、events 修复、CLI 一致性、chopin run 配方；P0/P1/P2 分切片）** | 用户决策；本轮真机任务与双审计证据 |
| 2026-11-18 | **新增 D28：低延迟观测（helper 快帧/JPEG、frame 字节、observe 组合、帧复用/缓存、HUD ACK、事件驱动 wait）、一条命令初始化（chopin init、set-sim、动画基线）与 /doc/skill 下载** | 用户决策；真机延迟实测见 D28 背景 |
| 2026-11-18 | **工作区收敛为远控专用**：移除 `chopin-compat` / `miui14-agent-tune` 与旧文档归档；ROM/反编译/移植研究资料迁到 `/root/chopin-research/`；DESIGN/AGENTS/README/WORKSPACE 重写 | 用户决策；本文件 |
| 2026-11-18 | **新增 D27：模块 Chopin Core / 软件 Chopin Pilot，包名与模块 id 一次性迁移** | 用户决策；本文件 §6.1 D27 |
| 2026-11-18 | **D26 修订：图案凭据改为工作区本地文件（0600，gitignore），不入 Git；构建时校验并打包** | 用户授权排查；安全取舍 |
| 2026-11-17 | S9–S14：通话、日常设置、WebUI/手机页、音频、图标/入口/布局、通话上行注入 | 真机验收记录见 `docs/TASKS.md` |
| 2026-11-16 | **新增 D20–D26：设备端单制品、root 宿主改为模块（删除 allowlist 授予层）、Go 单二进制、status.json 状态真源、adb 配对首次鉴权、能力不降级、单用户凭据例外** | 用户决策（重设计访谈 2026-11-16）；本文件整份重写 |
| 2026-11-15 | 新增 D16/D17：内嵌 WebUI 与 HUD（能力合同保留） | 用户决策 |
| 2026-11-15 | 新增 D15：IME `InputConnection` 提交与校验（合同保留） | 微信真机实测 |
| 2026-11-14 | 建立设计基线；记录设备与迁移决策 | 本文件；`docs/TASKS.md` |

## 验收门禁

`chopin acceptance` 只回答「端点能不能应答」，不足以保证「功能对不对」——这套代码里真实
发生过「返回 `ok:true` 但什么也没做」（`--clear`）、「有实现有单测但生产代码零调用」
（`ime.Restore`）、「返回 200 但发错了键值」（`global home` 发的是 `KEYCODE_MOVE_END`）。

因此验收分两层，判定规则见 [`docs/ACCEPTANCE.md`](ACCEPTANCE.md)：

- **能力矩阵** `chopin acceptance`：端点级自洽性；
- **使用流程** `chopin journey`：20 步，每步都必须同时满足「像用户那样操作」「换一条通道复核」
  「审计日志确认预期路径真的跑了」三条。

一个能力算 PASS 的前提是三条同时成立；SKIP 永远不算 PASS；不许用 `sleep` 当同步；
不许留下副作用。日志里没有预期那行就是 FAIL，即使界面看起来对。
