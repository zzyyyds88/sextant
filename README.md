# Sextant — 手机远控

> 当前方向（DESIGN D20–D37）：**设备端单制品**。
> KernelSU 模块 `chopin-core` 是唯一设备端交付物，开机以 root 直接拉起唯一 Go 守护进程；
> 客户端 APK 零 root；电脑侧用 `chopin` 命令驱动。
> 不刷镜像、不做 ROM 移植、不做源码级重建。
>
> **本工作区只负责远控。** ROM/反编译/移植等研究资料在 `/root/chopin-research/`，不属于交付物；
> 需要查证据时去那里看。
>
> 命名：**Sextant** 是项目展示名；CLI 仍为 `chopin`，设备端模块仍为 `chopin-core`，APK 包名仍为 `com.chopin.pilot`。技术标识在迁移前保持不变。

## 文档入口

| 文档 | 作用 |
|---|---|
| [`docs/DESIGN.md`](docs/DESIGN.md) | **唯一设计真源**：目标、owner 表、ADR D6–D37、配置/API/客户端合同、失败模式、切片与验收门禁 |
| [`docs/TASKS.md`](docs/TASKS.md) | 任务切片状态与真机验收记录（状态，不是设计真源） |
| [`docs/ACCEPTANCE.md`](docs/ACCEPTANCE.md) | 验收流程与判定规则：能力矩阵 + 使用流程 + 三条判定规则 |
| [`docs/RELEASE.md`](docs/RELEASE.md) | 签名、校验和与发布检查清单 |
| [`AGENTS.md`](AGENTS.md) | agent 协作宪法：真源优先级、推理闸、适配块、验收与 Git 规则 |
| [`modules/chopin-core/README.md`](modules/chopin-core/README.md) | 设备端制品：安装、配置、ctl 子命令、排障 |
| [`apps/pilot/README.md`](apps/pilot/README.md) | 客户端 APK：状态页 / HUD / IME（零 root） |
| [`apps/pc-cli/README.md`](apps/pc-cli/README.md) | 电脑侧 `chopin` 命令：配对、健康检查、能力调用、验收 |
| [`PRODUCT.md`](PRODUCT.md) | 产品语气、界面取舍与受众 |
| [`WORKSPACE.md`](WORKSPACE.md) | 本机工作区与外部研究资料边界 |

## 交给 AI 使用

AI 的入口是项目内的 agent 手册，不是这份 README：

```bash
chopin skill                    # 打印完整手册
chopin skill --install DIR      # 安装到 agent 的 skills 目录
chopin skill --zip -o skill.zip # 打包
```

未配对时也可以直接从设备读公开文档：

```bash
curl -k https://<device>:8443/doc/skill
curl -k https://<device>:8443/doc
```

把下面这段直接交给 agent 作为启动提示词：

```text
你通过 chopin CLI 控制一台 Android 手机。按顺序工作：

1. 运行 `chopin doctor`。任一项失败就报告原文和复现命令，不要猜。
2. 阅读 `chopin skill`（或 GET /doc/skill），遵守其中的原语、错误语义和配方格式。
3. 感知用 `chopin page` / `tree` / `observe`；操作前 `chopin claim`，用
   `chopin act` / `list` / `readback` 并验证结果，不要以“命令返回 ok”代替生效。
4. 长任务用 `chopin heartbeat` 保活；结束用 `chopin release`，它会释放会话并锁屏收尾。
5. 不绕过 HUD/handover；不要把 SKIP 当 PASS；不要伪造成功。
```

`claim` 会把本次会话写到 `~/.chopin/session.json`（0600），后续命令自动带
`X-Chopin-Session`；`release` 删除。业务操作会刷新任务活动时间，心跳只保活；
默认 300 秒无业务操作会被守护回收并锁屏。

## 快速开始

构建：

```bash
tools/scripts/build-core.sh                        # Go 守护 + ctl（同一 ELF，linux/arm64）
apps/pilot/build.sh                                 # 客户端 APK
apps/pc-cli/build.sh                                # PC CLI
tools/scripts/build-module.sh modules/chopin-core   # → modules/chopin-core.zip
```

公开仓库不包含 `tools/go`、Android SDK、Kotlin、`scrcpy-server` 和已构建的模块 `lib/`。
按脚本头设置 `GO`、`ANDROID_SDK_ROOT`、`ANDROID_BUILD_TOOLS`、`ANDROID_PLATFORM`、
`KOTLINC`、`KOTLIN_STDLIB`；把 `scrcpy-server.jar` 放到 `apps/pilot/lib/`。
`build-module.sh` 还要求本地 `modules/chopin-core/config/unlock-secret`（0600，不入库）。

首次装机与配对（只在验证启动路径时重启）：

```bash
tools/scripts/install-device.sh
chopin pair --serial <adb-serial>
chopin health
```

没有内置默认 serial；用 `--serial` 或环境变量 `CHOPIN_SERIAL` 指定。

改完守护侧代码，不重启就生效：

```bash
tools/scripts/activate-device.sh
```

验收：

```bash
tools/scripts/run-all-static.sh   # 静态门禁（编译与契约，≤60 项）
chopin acceptance                 # 端点级能力矩阵
chopin journey                    # 用户流程，三条判定
```

证据包落在 `out/acceptance-<ts>/REPORT.md` 与 `out/journey-<ts>/REPORT.md`；
验收规则见 `docs/ACCEPTANCE.md`；真机验收证据保留在私有开发仓库。

## 交付物

| 产物 | 路径 | 说明 |
|---|---|---|
| 设备端模块 | `modules/chopin-core.zip` | 唯一交付给设备的东西：Go 守护 + ctl、root helper、scrcpy-server、WebUI、配置，以及它负责安装的 APK |
| 客户端 APK | `apps/pilot/dist/chopin-pilot-<version>.apk` | 状态页 + HUD + IME；零 root |
| 电脑侧 CLI | `apps/pc-cli/dist/chopin-linux-amd64`（Windows 为 `chopin-windows-amd64.exe`） | 配对、健康、能力调用、验收导出 |

## 安全与发布

- 开发构建用 `apps/pilot/build/keystore/` 的本地自签名 key；发布使用仓库外的专用 release key（证书 SHA-256 `3bd6c103…`），升级必须复用同一 key。两者都不是公开 CA 身份。
- 发布资产附 `SHA256SUMS` 与 GPG 分离签名；公钥见 [`docs/release-key.asc`](docs/release-key.asc)，指纹 `61D77E…`，验证命令见 [`docs/RELEASE.md`](docs/RELEASE.md)。
- keystore、密码文件、含凭据的构建日志一律不发布、不提交。
- 仓库不提交预构建 release；发布资产由维护者按 `docs/RELEASE.md` 生成。
- 换 release key 首次升级需要卸载旧 APK 一次（丢 APK 数据，守护状态不受影响），见 `docs/RELEASE.md`。

## 参考设备

当前实现与验收基线是 chopin（Redmi Note 10 Pro 5G / POCO X3 GT），MIUI 14 / Android 13，
KernelSU 3.1.8。协议、模块结构与 CLI 可以迁移到其他 KernelSU 设备，但 a11y、IME、
priv-app 安装和 MIUI 后台策略都是按这个 ROM 实测的；换设备必须重新跑
`acceptance` 与 `journey`，不能直接宣称兼容。

本仓库不包含设备序列号、ADB key 或运行时凭据；ADB 连接串使用
`<adb-serial>` 占位，服务端口为 `8443`（TLS + Bearer token）。

## 仓库目录

```text
sextant/
├── docs/
│   ├── DESIGN.md              # 唯一设计真源（D6–D37）
│   ├── TASKS.md               # 切片状态 + 真机验收记录
│   ├── ACCEPTANCE.md          # 验收流程与判定规则
│   └── RELEASE.md             # 签名与发布检查清单
├── modules/
│   └── chopin-core/           # Go 守护 + ctl + helper + scrcpy + webui + 配置
├── apps/
│   ├── pilot/                 # 客户端 APK：状态页 + HUD + IME（零 root）
│   └── pc-cli/                # 电脑侧 chopin 命令（Go）
├── tools/
│   ├── scripts/               # 构建 / 静态门禁 / 激活 / 装机 / 验收 / WebUI 检查
│   ├── go/ android-sdk/ kotlin/   # 工具链（公开快照不含）
│   ├── probe/                 # WebRTC 端到端探测器
│   └── webui-check/           # /ui 无头浏览器检查
└── adbhome/                   # 独立 HOME + 授权 adbkey（公开快照不含）
```

## 外部研究资料

不在本仓库，固定在 `/root/chopin-research/`：

| 路径 | 内容 |
|---|---|
| `decompiled/` | JADX/APKTool 反编译树、索引、系统应用清单 |
| `downloads/` | 官方 MIUI recovery ROM zip |
| `extract/` | `payload.bin` 解包与分区镜像 |
| `old/` | 旧设备/旧构建归档 |
| `research/` | LineageOS、XMAPort、Shizuku、SukiSU allowlist、dsh-go、scrcpy、MIUI a11y 保活实证 |
| `audit/` | 设备侧环境审计截图/XML |
| `tools/` | JADX、magiskboot、APK 样本与反编译脚本 |

这些资料只用于查证据；静态反编译不等于运行时行为，ROM/移植结论也不作为本工作区的设计真源。

## 设备侧

- ADB 使用独立 HOME：

  ```bash
  HOME=<workspace>/adbhome adb -s <adb-serial> ...
  ```

- 模块运行时目录：`/data/adb/chopin-core/`（配置、状态、日志、凭据、基线）。
- 模块 ctl：`/data/adb/ksu/bin/chopin-core`（KernelSU 把该目录放进 `su` 的 PATH）。
