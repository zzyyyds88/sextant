# 工作区说明（WORKSPACE）

> 状态：**远控专用工作区**（2026-11-18 收敛）
> 当前方向：设备端单制品 —— KernelSU 模块以 root 拉起唯一 Go 守护（DESIGN D20–D37）
> 本机工作区：`/root/chopin-ksu`
> 外部研究资料：`/root/chopin-research/`（不纳入 Git，不是产品真源）

## 本机路径

```text
/root/chopin-ksu          # 本工作区：只放远控主线
/root/chopin-research     # 外部研究资料：ROM、反编译、移植、旧构建、参考项目
```

## 工作区内容

- `modules/chopin-core/`：唯一设备端制品 —— Go 守护 + ctl + helper + scrcpy + webui + 配置。
- `apps/pilot/`：零 root APK —— 状态页 + HUD + IME。
- `apps/pc-cli/`：电脑侧 `chopin` 命令 —— 配对、能力调用、验收。
- `docs/`：当前设计真源（`DESIGN.md`）、任务与真机验收（`TASKS.md`）、验收规则（`ACCEPTANCE.md`）；真机验收证据不随公开仓库发布。
- `tools/scripts/`：构建、静态门禁、跨产物契约、文档漂移、设备激活/装机/验收、WebUI 检查。
- `tools/probe/`、`tools/webui-check/`：远控链路验证工具。
- `adbhome/`：独立 HOME + 授权 ADB key（0600，不提交）。
- `out/`：验收/调试产物（ignored）。

本工作区**不包含**：

- 第二个 KSU 模块（`chopin-compat`、`miui14-agent-tune` 已移出）；
- 旧设计归档（历史由 Git 记录）；
- ROM zip、payload/分区镜像、反编译树、移植调研、旧构建。

## 外部研究资料

`/root/chopin-research/` 只用于查证据，**不是产品真源**，也不参与构建：

| 路径 | 内容 |
|---|---|
| `decompiled/` | JADX/APKTool 全量树、索引、系统应用清单 |
| `downloads/` | 官方 MIUI recovery ROM zip（4.0GB） |
| `extract/` | `payload.bin` 解包与分区镜像 |
| `old/` | 旧设备/旧构建归档 |
| `research/` | LineageOS、XMAPort、Shizuku、SukiSU allowlist、dsh-go、scrcpy、MIUI a11y 保活实证 |
| `audit/` | 设备侧环境审计截图/XML |
| `tools/` | JADX、magiskboot、APK 样本与反编译脚本 |
| `uploads/` | 旧临时输入 |

引用外部资料时必须在文档里写清路径，并区分“静态观察”和“当前运行时事实”。

## 工具链

- Go：`tools/go/bin/go`（守护/PC CLI 构建）。
- Android SDK：`tools/android-sdk/`（aapt2/d8/zipalign/apksigner）。
- Kotlin：`tools/kotlin/`（HUD）。
- JDK 21、Python 3.10、adb 1.0.41。
- 公开快照不包含上述工具链二进制；按脚本头设置 `GO`、`ANDROID_SDK_ROOT`、`KOTLINC` 等环境变量。
- 不用 Gradle；APK 由 `apps/pilot/build.sh` 走 aapt2 → javac/kotlinc → d8 → 打包 → zipalign → apksigner。

## 验证

```bash
tools/scripts/run-all-static.sh          # 27 项静态门禁
CHOPIN_SERIAL=<adb-serial> chopin pair
chopin acceptance                        # 能力矩阵
chopin journey                           # 使用流程
```

- 当前基线：静态门禁 27 项 0 失败；设备端守护、APK、helper 均在线。
- 真机验收证据保留在私有开发仓库；判定规则见 `docs/ACCEPTANCE.md`。
- 设备序列用 `--serial` 或 `CHOPIN_SERIAL` 指定；脚本用 `SERIAL` 覆盖。

## 敏感材料

- `adbhome/.android/adbkey`（0600）：不打印、不提交、不复制到公开位置。
- `modules/chopin-core/config/unlock-secret`：图案凭据（0600），工作区本地文件，**不入 Git**；构建时由 `build-module.sh` 校验并打包进模块 zip；运行时不进 argv/日志/API/审计。
- `apps/pilot/build/keystore/`：签名密钥（ignored，0600），升级必须复用。
