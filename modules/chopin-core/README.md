# Chopin Core（chopin-core，设备端制品）

chopin 的**唯一设备端交付物**：一个 KernelSU 模块。模块开机以 root 拉起唯一 Go 守护进程，
它负责全部特权语义（无障碍绑定与保活、MIUI 后台/权限放行、解锁、输入注入、截图/UI 树、
会话、状态、审计），并负责安装零 root 的客户端 APK。

设计真源：`docs/DESIGN.md`（D20–D34）。本文件只讲怎么用。

## 安装

```bash
tools/scripts/build-core.sh            # Go 守护（linux/arm64）
tools/scripts/build-helper.sh             # root helper dex
apps/pilot/build.sh && cp apps/pilot/dist/chopin-pilot-*.apk modules/chopin-core/lib/app.apk
tools/scripts/build-module.sh modules/chopin-core   # → modules/chopin-core.zip
# 凭据是本地构建输入，不入 Git：构建前确保 config/unlock-secret 存在（0600）；
# build-module.sh 会在缺失/为空时显式失败。

adb push modules/chopin-core.zip /data/local/tmp/
adb shell su -c 'ksud module install /data/local/tmp/chopin-core.zip'
# 重启一次。守护由 service.sh 以 root 拉起；没有 allowlist / 没有"再重启一次"步骤。
```

> 一步到位：`tools/scripts/install-device.sh`（zip → 模块安装 → 重启 → 等守护应答 → 配对）。
> `--no-reboot` 免重启激活；`--rollback` 卸模块与 APK。

## 开机后发生了什么

1. `post-fs-data.sh`（framework 未起）：只建目录、定权限、轮转日志。
2. `service.sh`（`sys.boot_completed=1` 之后）：播种配置、校验解锁凭据、
   md5 比对后 `pm install -r` 内置 APK、把 CLI 软链到 `/data/adb/ksu/bin`、
   最后以 root `exec` 守护进程。
3. 守护：拿单实例锁、自签证书、发布 `status.json`、启动 helper 与触发器、
   开始 a11y/后台/权限保活循环。

守护崩溃由模块脚本重启；手机杀掉整个 App 也不影响守护（它是 root 进程）。

## 配对（第一次从电脑连上来）

```bash
adb shell su -c 'chopin-core ctl pair new --agent pc'   # 打印一次性码，5 分钟有效
# 或一条命令完成（推荐，见 apps/pc-cli）：
chopin pair --serial <adb-serial>
```

配对码一次性、TTL 5 分钟、错误 3 次锁定。设备只存 token 的 SHA-256。

## ctl 子命令

| 命令 | 作用 |
|---|---|
| `chopin-core ctl version` | 版本与 schema |
| `chopin-core ctl status [--json]` | 打印守护发布的状态（读 `state/status.json`） |
| `chopin-core ctl pair new --agent NAME` | 生成一次性配对码 |
| `chopin-core ctl token list` | 已签发 token 的元数据（不含明文/哈希） |
| `chopin-core ctl token revoke --agent NAME` | 吊销某 agent 的全部 token |
| `chopin-core ctl token rotate --agent NAME` | 换发（打印新 token 一次） |
| `chopin-core ctl secret set-unlock` | 从 **stdin** 读图案并写 0600 |
| `chopin-core ctl secret clear-unlock` | 删除图案 |
| `chopin-core ctl secret set-sim` | 从 **stdin** 读 SIM PIN（4-8 位数字）并写 0600 |
| `chopin-core ctl secret clear-sim` | 删除 SIM PIN |
| `chopin-core ctl secret status` | 只报告存在与否 |
| `chopin-core ctl animation off` | 关闭窗口动画并记录基线 |
| `chopin-core ctl animation restore` | 按基线还原动画 |
| `chopin-core ctl animation status` | 读三项动画缩放值 |
| `chopin-core ctl log [n]` | 最近 n 行审计日志 |


## 运行时目录

`/data/adb/chopin-core/`

| 路径 | 内容 |
|---|---|
| `config/core.json` | 唯一配置（首次安装从模块复制，之后升级不覆盖） |
| `config/unlock-secret` | 图案凭据（0600，root） |
| `state/status.json` | 状态真源（原子替换写） |
| `state/{engine.lock,engine.pid,tokens.json,pairing.json}` | 单实例、token、配对 |
| `state/baseline/` | 写入前基线（回退依据） |
| `log/core.log` | 审计与运行日志（1 MiB 轮转） |
| `app/` | 发给 APK 的副本：`status.json`、`api.json`、`app-token`（chown 到 app uid） |
| `tmp/` | 截图与 UI dump 中间产物 |

## 边界

- 只操作本项目的目标包（配置里的 `app.package`）。清单外一律不碰。
- 不 `pm uninstall` / `disable-user` 预装应用；不刷分区、不改 boot。
- 状态只来自真实读取（`dumpsys`/`settings get`/`cmd appops get`/`pm list`），不伪造。
- 卸载：`ksud module uninstall chopin-core` → 停守护、卸 APK、还原 a11y、删运行目录，
  最后一份日志留在 `/data/local/tmp/chopin-core-last.log`。

## 许可

AGPL-3.0。
