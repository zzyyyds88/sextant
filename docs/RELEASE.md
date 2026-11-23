# 发布与签名（RELEASE）

这份文档只讲发布：签名 key、校验和、上传前检查。设计真源仍是 `docs/DESIGN.md`。

## 当前签名是什么

开发构建与发布构建用两套 key：

| 用途 | keystore | 证书 SHA-256 | 说明 |
|---|---|---|---|
| 本地开发 | `apps/pilot/build/keystore/keystore.jks` | `e4466d6d16b31d1fd69e0dc5964057b85420677d87f627637276b1cc1ce88fe5` | 自动生成，RSA 2048，DN 历史 `CN=com.chopin.remote`，只用于日常构建 |
| 发布 | `~/.chopin-release/chopin-release.jks` | `3bd6c1036a06e85b59beea2bc62b870118fc77b2167fd1b11ec9d7216456ad5d` | 专用，RSA 4096，有效期 36500 天，DN `CN=com.chopin.pilot` |

- 发布 key 的密码在 `~/.chopin-release-secrets/keystore.pass`（0600），与 keystore 分目录。
- 两者都是**本地自签名**，不是公开 CA。Android 只要求“升级用同一 key”；发布时必须公布证书 SHA-256，让用户核对。
- 发布资产再用 GPG 分离签名：
  - 公钥：`docs/release-key.asc`
  - 指纹：`61D77EFA0C40FD2F01BEB5AEA395B798CB069049`
  - 私钥在 `~/.chopin-release/gnupg`（0700），密码在 `~/.chopin-release-secrets/gpg.pass`（0600）。

## 已知风险

1. keystore、密码文件与 GPG 私钥只能留在构建机上；权限 0600，离线备份，不要放在同一磁盘。
2. 丢失 keystore 后无法覆盖升级，只能卸载（会丢应用数据）。
3. 模块 zip 没有 APK 之外的签名；发布时用 `SHA256SUMS` + GPG 签名保证。
4. 公开仓库或 release 前，清理 README、截图、日志里的内网 IP、设备标识、token。

## 构建 release

```bash
CHOPIN_RELEASE=1 \
CHOPIN_KEYSTORE="$HOME/.chopin-release/chopin-release.jks" \
CHOPIN_KEYSTORE_PASS_FILE="$HOME/.chopin-release-secrets/keystore.pass" \
apps/pilot/build.sh
```

输出：

- `apps/pilot/dist/chopin-pilot-<version>.apk`
- 同名 `.sha256`
- 证书 DN 与 SHA-256 指纹

release 模式不会自动生成 key；缺 `CHOPIN_KEYSTORE`、缺 `CHOPIN_KEYSTORE_PASS_FILE` 或文件不存在都会失败。密码通过 `env:` 传给 `apksigner`，不出现在 argv；仍不要执行 `set -x`，也不要把密码写进构建日志。

## 生成发布包

```bash
tools/scripts/build-module.sh modules/chopin-core
REL=out/release-3.0.0
mkdir -p "$REL"
cp apps/pilot/dist/chopin-pilot-3.0.0.apk "$REL"/
cp modules/chopin-core.zip "$REL"/
cp apps/pc-cli/dist/chopin-linux-amd64 apps/pc-cli/dist/chopin-windows-amd64.exe "$REL"/
cp docs/release-key.asc "$REL"/
( cd "$REL" && sha256sum chopin-pilot-3.0.0.apk chopin-core.zip \
    chopin-linux-amd64 chopin-windows-amd64.exe > SHA256SUMS )
GNUPGHOME="$HOME/.chopin-release/gnupg" gpg --batch --pinentry-mode loopback \
  --passphrase-file "$HOME/.chopin-release-secrets/gpg.pass" \
  --armor --detach-sign --output "$REL/SHA256SUMS.asc" "$REL/SHA256SUMS"
```

## 用户如何验证

```bash
gpg --import release-key.asc
gpg --verify SHA256SUMS.asc SHA256SUMS
sha256sum -c SHA256SUMS
apksigner verify --print-certs chopin-pilot-3.0.0.apk
```

证书 SHA-256 必须是 `3bd6c103…`；GPG 指纹必须是 `61D77E…`。不一致就不要安装。

## 换 key 的首次升级

设备上已安装的 APK 是开发 key（`e4466d…`），发布 APK 是 `3bd6c1…`。Android 不允许签名不同的覆盖安装：

- 首次换到 release APK 必须先卸载 `com.chopin.pilot`（本项目自己的包，允许），再安装 release APK 或刷入包含它的模块。
- 卸载会丢 APK 的数据（状态页偏好等）；守护的 `/data/adb/chopin-core` 状态不受影响。
- 模块 `service.sh` 只做 `pm install -r`，不会自动卸载；换 key 必须人工做一次。
- 如果不愿意卸载，就继续使用开发 key，但那不是 release key。

## 发布前检查

- [ ] `apksigner verify --print-certs <apk>` 通过；证书 SHA-256 与上一版相同（同 key）。
- [ ] `gpg --verify SHA256SUMS.asc SHA256SUMS` 通过；公钥指纹正确。
- [ ] `sha256sum -c` 全部通过。
- [ ] 模块 zip 内的 `lib/app.apk` 与发布的 APK 同一证书。
- [ ] 换 key 的首次升级已按上面的卸载步骤验证。
- [ ] `git status` 干净；模块 zip 与当前 commit 对应。
- [ ] README、截图、日志没有内网 IP、设备标识、token、keystore。
- [ ] release notes 写明：自签名指纹、GPG 指纹、安装/升级方式、回退代价（卸载会丢应用数据）。

## 发布状态

- 本仓库只包含源码、脚本、文档与 GPG 公钥，不提交 keystore、密码或预构建发布资产。
- 预构建 release 由维护者按上面的命令生成；上传前必须完成“旧开发 key → release key”的设备迁移演练（需要卸载一次）。
