# Sextant 协作规则（AGENTS.md）

> 本文件约束 agent 在本项目中的工作方式。项目展示名：**Sextant**；项目对象：chopin（Redmi Note 10 Pro 5G / POCO X3 GT，<device-model>）的 MIUI 14 **远控**定制——设备端 KernelSU 模块 `sextant-core` + 零 root 客户端 `Sextant Pilot` + 电脑侧 `sextant` CLI。
> 优先级：低于 `docs/DESIGN.md`（唯一设计真源），高于任何临时指令和旧会话记忆。
> 本文件按 2026-11-18 新协助标准重写；模板中的通用规则与项目专有规则合并，不保留占位符。
> 任何时候使用中文，除非用户明确要求英文输出。

## 0. 项目标识

| 项 | 值 |
|---|---|
| 项目 | **Sextant**（CLI `sextant`、模块 `sextant-core`、APK `com.sextant.pilot`；旧 `chopin-*` 标识按 D38 迁移后删除）——只负责远控；不维护其他 KSU 模块、不做 ROM 移植/镜像 |
| 设计真源 | `docs/DESIGN.md`（D6–D38；D20–D38 为当前形态） |
| 任务状态 | `docs/TASKS.md`（状态与真机验收记录，不是设计真源） |
| 验收规则 | `docs/ACCEPTANCE.md`（三条判定：像用户操作、换通道复核、读审计日志） |
| Owner | 设备端特权语义唯一 owner：`modules/sextant-core/src`（Go 守护） |
| 构建 | `tools/scripts/build-core.sh` → `tools/scripts/build-module.sh modules/sextant-core`；`apps/pilot/build.sh`；`apps/pc-cli/build.sh` |
| 验收命令 | `tools/scripts/run-all-static.sh`；`sextant acceptance`；`sextant journey`；`tools/scripts/device-acceptance.sh` |
| 设备 | `adb -s <adb-serial>`（<device-model> / MIUI 14 / Android 13 / KernelSU 3.1.8） |
| 外部研究 | `/root/chopin-research/`（只查证据，不是交付物，不是设计真源） |

## 1. 固定习惯（跨项目恒定）

| 用户通常这样做 | agent 必须这样做 |
|---|---|
| 指令很短，常见“改一下”“继续”“这个不行” | 先补全成可执行步骤再动手；只能补细节，不得替换目标；无法补全时先提问，可问多个具体问题，但不得用提问代替执行 |
| 用“不要 / 别 / 不用”表达需求 | 禁止项一旦出现即硬约束，仅当轮生效；若用户要求固化为项目规则，写入 `AGENTS.md`/设计文档后才在后续轮次延续 |
| 依据截图、界面、看图判断成果 | 先真实打开再下结论；界面类结论必须配截图或可复现步骤 |
| 要求把纠正写进工作流或记忆 | 同一纠正第二次出现即视为规则：写入 `AGENTS.md` / 工作流 / 记忆文件，并回执“已写入 X，生效范围 Y” |
| 同时推进多个项目 | 一次会话只服务一个项目路径；跨项目只借方法，不改文件、不串约定 |
| 不参与技术选型 | 给一个推荐方案并直接执行；何时停问按 §11 |

## 2. 硬规则（所有任务恒定生效）

1. **只读默认**。没有明确说“改 / 修 / 实现 / 替换 / 删除”就是只读：只读文件、跑只读命令、给判断。用户说“先看 / 先判断 / 审计 / 你觉得”一律只读，直到明确要求执行。出现“只要 / 仅 / 只改 / 你只能改这些文件”时，只动被点名的对象；白名单之外一律只读，发现问题只报告，不顺手重构、不扩散清理。
2. **复用优先**。先找项目已有的脚本、入口、命令、虚拟环境、工具函数；存在就不新建，不够用就最小修改原文件。项目内没有现成实现时，先在 GitHub 等公开来源检索可复用方案，优先复用而不是从零写；选用外部方案时说明来源、许可证与改动点。禁止另起炉灶式的新脚本、新目录、新流程。
3. **环境固定**。解释器、包管理器、构建/部署/测试命令取自项目真源（README、脚本头、lock 文件、CI）；项目有 venv / conda / 容器就用它。禁止用系统默认解释器替代，禁止猜命令。同一命令因环境（shell、镜像路径、缓存）连续失败时，按 §2-6 的失败处理改道，不得重复重试。
4. **参数不漂移**。用户确认过的执行参数（有头/无头、并发数、模型、路径、账号、超时、输出格式）逐字沿用。要改先说明“原参数 X，改为 Y，因为 Z”。
5. **声称纪律**。“已启动 / 已完成 / 已修复 / 已通过”只能配证据出现：进程 PID、命令输出、测试结果、日志行、diff、截图、接口响应。没有证据就说“未开始 / 进行到 X / 未验证”。
6. **不兜底**。异常直接暴露：报错原文 + 复现命令 + 影响面。禁止吞错、默认值掩盖、静默降级、mock 假数据。同一失败重试两次即停止，换方案或报告。项目 D25 的“能力不降级”是这条在远控能力上的具体化：不可用返回结构化错误，不做只读兜底/假成功。
7. **过程文件归位**。临时脚本、调试文件、中间报告、一次性截图用完删除；确需保留的放进项目约定的过程目录，并说明保留原因，不得散落在仓库根或工作目录。用户没要的交付物不要塞进仓库。
8. **单真源执行**。设计、合同、文案模板、工作流、话术按 §14 的真源逐条执行，不得自行增删步骤或改写既定表述。跨入口语义先进共享合同或 owner 层，不散落在 UI、脚本、prompt。
9. **结论在后**。先给过程与证据，最后给结论。不写空话、不重复用户原话、不用 emoji、非必要不空行。
10. **依赖先换源**。GitHub 克隆、raw 文件、Release 资产、Docker 镜像走 §13 的加速地址；pip / uv / npm 安装前切到 §13 的源。同一源连续失败两次才回官方源，并说明换了哪个源；禁止在默认源上反复重试，其余失败按 §2-6 处理。
11. **敢说不对**。发现需求不合理、架构方向错误、规则冲突或验收条件缺失时，先指出问题并给证据、影响面和替代方案，不机械执行。
12. **禁止补丁式开发与选项剧场**。不得用散落 `if`、临时兼容分支、硬编码、`sleep`、全局 flag、prompt 文案、mock 或假数据绕开架构问题；给推荐主线，备选方案只用于解释为什么不选；禁止无意义向后兼容，旧字段/旧接口污染当前合同时先说明取舍。

## 3. 开工前确认与推理闸

开工前先确认三件事：

1. **真源在哪**：按 §14 真源优先级确认；没有指定真源时，当前源码、测试、配置、日志、git 状态就是真源。
2. **owner 在哪**：设备端特权语义归 `modules/sextant-core/src` 管；`apps/pilot` 只做状态展示/HUD/IME/凭据录入；`apps/pc-cli` 只做协议映射；模块 shell 只做生命周期。adapter / UI / CLI / shell 不拥有业务语义，不私造状态与结论。
3. **验收怎么算过**：`tools/scripts/run-all-static.sh` + 对应真机验收（`sextant acceptance` / `sextant journey` / 设备证据）。三者拿不到就先审计并说明缺什么，不得边猜边做。

编码或执行高风险操作前，推理闸至少回答：

1. 实际要解决的问题是什么？
2. 谁创建这个概念，谁调用它，谁消费它？
3. 当前真源在哪里？是否已经有同职责模块、合同、schema、脚本、配置或文档？
4. 唯一 owner 是哪一层？哪些层禁止成为 owner？
5. 是否跨 UI / API / 引擎 / 模块 / 脚本 / 设备侧多个入口？跨入口语义是否已进入共享合同或核心层？
6. 更简单、更保守的设计是什么？为什么不够？
7. 最大回归风险是什么？用什么测试、日志、命令、截图或实机证据阻断？
8. 本轮实现是否严格来自当前设计？设计是否完整覆盖当前范围？未覆盖时是否已提出设计修订提案并等待确认？
9. 是否涉及刷机、分区、vbmeta/AVB、BL、数据清除、凭据、设备不可逆状态或预装应用？是否有用户明确授权、回退路径和验收步骤？
10. 是否引入第二条平行命令面、第二个状态真源、第二种错误表达方式？如果是，先说清为什么不复用现有的。

推理闸没有闭合，不开始实现。

## 4. 项目边界

- **做**：KernelSU 模块（Go 守护 + 少量生命周期 shell + systemless overlay / settings / AppOps / 组件）+ 零 root APK + PC CLI；基于真机证据；模块安装/卸载可逆。
- **不做**：刷镜像、改 system/vendor 分区、动 vbmeta/AVB、relock BL、清用户数据、`pm uninstall` / `disable-user` 预装应用（唯一例外是本项目自己的 `com.sextant.*`）、镜像重打包、源码级 MIUI/HyperOS 重建、在本机做全量 AOSP/LineageOS 编译、ROM 移植。
- **已否定方向**：把能力拆成「权限层模块 + 独立远控软件」两套（D18/D19，已由 D20 取代）；APK 持 root 再拉起引擎（D21 取代）；用 KernelSU allowlist 给 APK 授予 root（D21 删除）；让 APK 解析 CLI 文本读状态（D23 取代）；`.95` 侧远程代解锁（改为设备本机解锁）；与守护平行的第二套命令面或第二个状态真源；能力降级/只读兜底/假成功（D25 禁止）。
- **不保留旧版本回退**：D27 的 Core/Pilot 迁移已完成；旧模块 zip/APK 备份、旧路径迁移兜底、双版本兼容已删除。升级只支持 `sextant-core` 自身版本前进。
- 不为了速度删除对账、身份、权限、审计、验证、实机证据、用户可见状态或数据完整性字段。
- 所有外部输入、队列、缓存、请求体、响应体、会话窗口、文件大小、重试、超时和后台 worker 都要有上界、背压、释放或降级策略；禁止无界增长和永久常驻的隐性运行面。
- 用户明确否定过的概念必须从代码、文档、命名里删干净，不许换名保留。

## 5. 交付前自检

- 我做的是你说的事吗？有没有把 A 做成 B？
- 用的是项目既有入口吗？还是又建了一个平行实现？
- 我的“完成”有证据吗？还是只是我自己说完成了？
- 你确认过的参数我改了吗？
- 我留下临时文件了吗？工作区干净吗？
- 我是不是在等一个根本没在跑的东西？
- 本轮触达的调用链是否闭合到 owner、测试和文档？

## 6. 执行流程

| 级别 | 判定 | 流程 |
|---|---|---|
| 微任务 | 单文件、文案、样式、常量、明显 bug、已指到具体位置 | 直接改 → 验证 → 一句话说明 |
| 中任务 | 单模块、2–4 文件、不改跨模块契约 | 确认契约 → 改代码 → 补测试 → 改文档，可同次提交 |
| 大任务 | 跨模块、改公共契约 / schema / 状态机、新增子系统 | 先改设计并单独提交 `docs:` → 拆任务 → 分派实现 → 删拆分文档 |

- 只改实现、让它符合现有设计，属微/中任务，不必先改设计；暴露设计缺口时走提案回流，不许实现先行、设计后补。
- 设计一次写完整：无法确定的事项显式列为待决项，写明阻塞范围、可选方向、推荐方向；禁止隐式 TBD 与 MVP 式裁剪。
- 从合同、schema、模型、核心服务开始，不从 UI、HTTP handler、临时脚本倒推核心。
- 生成物只读不手改：找到源文件与生成命令，改源再重新生成。
- 抽象只在减少真实复杂度时引入。
- **最小改动不是半截改动**：修 bug 不顺手重构、加功能不扩散清理，但本轮触达的调用链必须闭合到 owner、测试和文档。
- **提案回流**：停下来给问题与证据、影响范围、与设计的冲突、可选方案、推荐方案、待确认问题；确认后先改设计并提交 `docs:`，再继续实现。禁止边写代码边暗改设计。
- **必需工作流**：当前真源审计 → 推理闸 → 唯一 owner 与合同设计 → 测试/fixture/门禁计划 → 核心实现 → 薄 adapter/UI/CLI/API/脚本接线 → 针对性验收 → 文档回写 → git 边界复核。
- **接管项目**：禁止把已有项目当空项目重写；先做只读接管审计，确认入口、调用链、契约、生成规则、测试脚本、配置、部署习惯、未提交变更和文档线索。
- 编写代码前先看 `git status --short`；不干净先处理。报错优先联网检索，禁止瞎猜；同一处修改一次仍失败则转检索。
- 任务拆解后尽可能并行分派子代理执行，审查其产出，不合格则打回重做，直至全部通过。

## 7. 验收

- 验收强度匹配风险：核心合同用单元/契约测试，跨入口用集成测试，UI 用真实浏览器截图与交互，部署用构建 + 端到端。
- 证据按 §2-5 提供。退出码 0 不等于功能生效，构建通过不等于界面正确，单测通过不等于端到端可用；三者只能证明各自那一层。
- 跑不了的关键验收说明原因与替代证据，不许隐瞒。
- 失败先复现、留证据、提可证伪假设，再改；禁止无复现地猜原因。
- warning、lint、类型告警、生成物漂移、文档索引缺失、本轮 TODO 都算缺陷，除非明确记为债务。
- **项目三条判定**：一个能力算 PASS，必须同时满足：
  1. 像用户那样操作（走公开 API/用户路径）；
  2. 换一条通道复核（a11y 输入用 OCR 复核；写设置读回；锁屏问 keyguard；截图下载比字节）；
  3. 读审计日志确认预期路径真的跑了（日志没有那行就是 FAIL）。
  SKIP 永远不算 PASS；不许用 `sleep` 当同步；不许留下副作用（设置改回、会话释放、临时文件删除）。
- **错误分级**：阻断（当轮收掉）/ 设计风险（说明取舍与验收入口）/ 可记录债务（说明原因）/ 无关优化（不进本轮）。没有证据的担忧不能写成阻断；有证据的阻断也不能降级成“后面再说”。

## 8. 子代理与批量任务

- 同构批量任务（同模板不同参数）一次派发完，禁止逐条等用户点名。
- 并发数按效率最大化自主决定，并说明选择理由；遇到账号、接口或平台限流时，降到不触发限制的最小值。
- 每个子代理必须带：范围、输入、禁止事项、输出格式。
- 主代理不空等：派发后做不重叠的工作，或明确说明只能等哪一步。
- 子代理结论不是真源，主代理对照代码、测试、日志复核后再采信。
- 子代理返回通道失败（ID 解析不到、消息不可用）时改在正文交付，并说明通道异常，禁止静默丢弃结果。

## 9. 输出与交付物

- 生成的文案、说明、话术：简洁直给，不堆形容词，不带动画式 emoji，非必要不空行；用户已有模板时严格套模板。
- 对外文档只写使用、配置、部署、排障；内部架构与治理写进 `docs/DESIGN.md`。
- 用户可见文案分两类，各有真源：HUD/无障碍（Kotlin 侧）走 `apps/pilot/app/res/values/strings.xml`；状态页（`MainActivity`）与引擎错误原因是纯 Java/Go 字面量，与 `strings.xml` 术语保持同一套说法。不要一半资源一半字面量地新造第三套文案。模块侧运维输出走 `sextant-core ctl` 命令面。
- 完成定义必须包含可验证结果、未闭合风险和下一步边界；禁止偷懒、糊弄和留尾巴。

## 10. Git、安全与仓库边界

- 默认分支 `main`。提交原子化、工作区绝对干净，一次提交只做一件事；前缀 `feat` / `fix` / `docs` / `refactor` / `test` / `chore` / `breakdown`。
- 提交信息用清晰的中文或约定式前缀；正文写清问题、根因、修法与回归，不要只写“修复 bug”。
- 写代码前先看 `git status --short`；不干净先处理，不混入用户未授权的改动。
- 禁止 `git add .`，只 stage 本任务文件；禁止 force-add ignored 文件，除非用户明确点名路径并要求。
- 公开仓库禁止提交密钥、令牌、私钥、真实用户数据、含凭据的 URL；私有仓库可提交，但必须先确认远程仓库确为私有且不会被公开。日志与报告同样不许泄露。
- 本项目 D26 的图案凭据是**工作区本地文件** `modules/sextant-core/config/unlock-secret`（0600，gitignore），构建时由 `build-module.sh` 校验并打包；**不入 Git**。运行时不进 argv/日志/API/审计。`adbhome/.android/adbkey`（0600）不打印、不提交、不复制。
- 主分支禁止 `reset --hard`、`push --force`、`clean -f`；功能分支允许 force push，但先确认没有他人在用该分支。其余破坏性命令仍需用户明确要求并确认风险。
- 嵌套仓库统一在根仓库提交，不单独提交子仓库；`reference/`、`.uploads/` 不进版本管理。本工作区当前没有嵌套仓库。
- 根仓库只跟踪远控主线：文档、脚本、配置、模块源码、APK/CLI 源码。不纳入 `adbhome/`、`tools/` 第三方工具链、`modules/*.zip`、`apps/pilot/dist/`、`apps/pilot/build/`、`apps/pilot/lib/scrcpy-server*`、`out/`、`.uploads/`。以 `.gitignore` 为准。
- 外部研究资料固定放在 `/root/chopin-research/`，不纳入 Git，不作为产品真源。
- 对外提 PR / issue 前先读贡献规则与已有讨论，一次只解决一个问题，说明 diff 范围与验证证据后再提交。
- 修改本文件、skill、系统提示或工作流模板前，必须说明现有规则为什么不够、会影响哪些行为，并用至少一个真实场景或压力测试验证改动有效；改完回执生效范围。

## 11. 必须停下来问

- 需求、代码、规则、真源互相冲突。
- 要改或删公开接口、schema、权限模型、部署流程、产品命名、agent 规则本身。
- 要做不可逆或大范围操作（批量删除、覆盖远端数据、真实发布、付费调用）。
- 需要无意义兼容或多路线并行。
- 上下文不足以判断 owner，且猜错代价不可逆。
- 实现中发现当前设计未覆盖、无法满足、互相冲突，或需要偏离设计才能继续。
- 需要刷机、清数据、改分区、relock BL、覆盖 boot/vbmeta、删除设备侧备份或使用未验证镜像。
- 需要读取、复制、打印或提交 ADB 私钥、keybox、签名材料、账号凭据、解锁凭据或其他敏感数据。

停下来时给：冲突证据、可选方向、推荐方向、需要确认的问题。可问多个问题，其余按推荐方案继续。停止时不要只说“无法继续”。

## 12. 适配块（只写项目真实使用且与通用规则不重复的约束）

### `platform/kernelsu-miui14`
applies_when: "改动 KernelSU 模块、boot 时序、无障碍设置键或 MIUI 后台策略"
authority: "低于 docs/DESIGN.md，高于普通建议"
verification: "tools/scripts/run-all-static.sh；tools/scripts/device-acceptance.sh；设备侧 `sextant-core ctl status`"

- **root 宿主是模块，不是 APK**：`service.sh` 由 KernelSU 以 root 运行（实测 uid 0、SELinux `u:r:su:s0`），直接 `exec` 守护。不要再引入 KernelSU allowlist 授予、不要再要求“重启一次才拿到 root”、不要给 APK root。
- 守护必须在锁屏下可用：`health`/`status`/`device`/`input` 不依赖 CE，不等待解锁；需要 CE 的动作（启动 Activity、HUD、APK 私有目录）在解锁后执行，未就绪返回结构化 `pending`。
- 同值写 `enabled_accessibility_services` 是 no-op；修复必须两步：先写不含目标的值，再写完整值。
- 真实 bound 的判据是 `dumpsys activity services <pkg>` 中 `hasBound=true` 且被 `system/1000` 绑定；`dumpsys accessibility` 的 `Bound services` 只有 label，settings 只是期望态。
- AMS 持久化短形式（`pkg/.Svc`）：比较前规范化为 `pkg/pkg.Svc`，否则越写越多重复。
- 锁屏/CE 不可用时不反复重写无障碍，也不把正常状态报成故障；锁屏态如实写 `PENDING` + 原因。
- `a11y` 与 `a11y_compat` 两个组件都必须真的绑定；只写 settings 不算。
- **priv-app overlay 不能单独运行**：这个 ROM 的 App 进程读不到 `/system/priv-app/SextantPilot/SextantPilot.apk`（ResourcesManager ENOENT → ContextImpl NPE）。必须同时保留 `/data/app` 更新副本；`service.sh` 在只有 priv-app 路径时也 `pm install -r`，包保持 `PRIVILEGED` + `UPDATED_SYSTEM_APP`。
- 守护先于首次解锁启动时，`/data/user/0/com.sextant.pilot` 不可用；`publishAppFiles` 必须在 keepalive 中重试，解锁后发布 `status.json`/`api.json`/`app-token`/`hud.json`。
- 模块必须保留 `uninstall.sh`、日志和回退路径（卸载可逆；旧版本回退不保留）；改 boot 行为前先备份 boot。
- 卸载必须可逆：写入前记录基线到 `state/baseline/`，`uninstall.sh` 停守护、卸 APK、按基线还原、保留最后一份日志到 `/data/local/tmp/`。
- 本工作区不再维护第二个 KSU 模块；不要引入 `chopin-compat` 式的 ROM 行为修正层或第二个 a11y owner。

### `language/posix-sh-android`
applies_when: "写或改 modules 下的 shell（只有生命周期脚本）"
authority: "低于 docs/DESIGN.md，高于普通建议"
verification: "sh -n + dash -n + busybox sh -n（run-all-static.sh 已含）"

- 模块 shell 只剩 `customize.sh` / `post-fs-data.sh` / `service.sh` / `uninstall.sh`。只做：目录与权限、按需 `pm install`、拉起/停止守护、日志轮转。禁止在 shell 里实现能力语义、状态判定、目标遍历、授权解析——那些属于 Go 守护（D22）。
- 只用 toybox / mksh 可用的语法；`set -eu`；`local` 不可依赖，用带前缀的变量名。
- 框架 CLI（`pm` / `settings` / `cmd` / `am` / `content`）一律重定向 `< /dev/null`。原因（真机证）：这些由 `app_process` 启动，fd0 会被带入 binder 事务；`system_server` 去 `read()` 一个 `/data/adb/**` 下的文件会被 SELinux 拒绝，报 `Failed transaction (2147483646)`、退出码 2。触发条件是文件标签（`adb_data_file`）而非内容。
- 循环读文件不得用 `done < 清单`：用独立 fd（`done 3< "$file"` + `read -r line <&3`），不要占用 stdin。
- 每条外部命令都要有退出码判断；禁止静默吞错，禁止用固定字符串代替真实失败原因。

### `language/go`
applies_when: "改动 modules/sextant-core/src（守护）或 apps/pc-cli"
authority: "低于 docs/DESIGN.md，高于普通建议"
verification: "GOPROXY=off go vet ./... && go test ./...；gofmt -l 必须为空"

- 守护是唯一特权语义 owner：a11y 绑定/保活、后台/权限、解锁、注入、截图、会话、状态、审计、音频都在这里实现一次；shell / APK / PC CLI 只消费。
- 依赖必须已在模块缓存里：`GOPROXY=off`，不要在构建脚本里临时联网拉包。现有依赖：`pion/webrtc`、`modernc.org/sqlite`。
- 单实例：启动时对 `state/engine.lock` 取 `flock(LOCK_EX|LOCK_NB)`，抢不到立即退出并写日志（不双开、不改写 pid/token）。
- 错误必须包裹上下文（`fmt.Errorf("...: %w", err)`），用 `errors.Is` 判定可恢复类型；生产路径禁止 `panic` 与裸字符串失败。
- 外部命令一律经可注入的 runner 执行，测试用 fake runner，不在单测里真的调 `su` / `adb` / `settings`。
- 状态写入必须原子替换（tmp + `rename`），读方永远看到完整 JSON；状态值只能来自真实读取（`dumpsys`/`settings get`/`appops get`/`pm list`），禁止固定值。
- 并发原语要能解释清楚为什么需要；单实例、限流、会话互斥要有对应单测。
- 改完必须 `gofmt`，未格式化的文件按缺陷处理。

### `language/java-no-gradle`
applies_when: "改动 apps/pilot/app（APK）"
authority: "低于 docs/DESIGN.md，高于普通建议"
verification: "apps/pilot/build.sh；apps/pilot/tools/verify-apk.sh"

- 没有 Gradle，构建是 `javac`/`kotlinc` + `d8` + `aapt2` + `apksigner`，入口 `apps/pilot/build.sh`（底层 `apps/pilot/assemble.sh`）。不要引入 Gradle/Maven 工程。
- **APK 零 root**：不得出现 `su`、不得申请 KernelSU 授权、不得拉起引擎进程、不得写 `enabled_accessibility_services` 或 allowlist。唯一允许的特权动作是经 `sextant-core ctl secret set-unlock` 写解锁凭据（值经 stdin，不读回）。
- 状态只能读 `state/app/status.json`（守护发布的副本），禁止解析任何 CLI 文本。字段与 `/v1/status` 完全一致，`schema` 不匹配要显式报错。
- IME 三字段必须分开：`ime_service`（`com.sextant.pilot/.ime.RemoteImeService`）与 `ime_provider`（`com.sextant.pilot.ime`）不是同一个字符串；混用会让文字注入永远失败。
- IME 输入视图是 48dp 高的 `TextView`；0/1dp 空 View 在 MIUI 上会让 insets 动画把目标应用内容截成空白。
- 反射调用系统隐藏 API 时必须写清依据（AOSP 方法语义 + 实机 A/B 证据）。`registerUiTestAutomationService` 必须带 `FLAG_DONT_SUPPRESS_ACCESSIBILITY_SERVICES(1)`，否则运行期间全机第三方无障碍服务被静默解绑；这条已搬到守护的 helper 侧，`check-contracts.sh` 继续锁着。
- 窗口层次/手势类能力必须区分“系统层面不可用”与“当前状态不可用”，并把原因结构化返回。
- 签名密钥在 `apps/pilot/build/keystore/`（gitignore，0600）：升级必须复用，否则无法覆盖安装；绝不提交。

### `device/adb`
applies_when: "通过 ADB、设备验收脚本或设备侧服务操作 chopin"
authority: "低于 docs/DESIGN.md，高于普通建议"
verification: "adb devices；tools/scripts/device-acceptance.sh；out/device-acceptance-* 证据包"

- 设备序列用 `--serial` 或 `SEXTANT_SERIAL` 指定；脚本用 `SERIAL` 覆盖。
- 首次鉴权走 adb 配对（D24）：`sextant pair --serial <adb>` 经 `adb shell su -c 'sextant-core ctl pair new'` 取一次性码再换 token。不得把 token 预置进仓库。
- 运行时凭据只经 stdin 写入 `/data/adb/sextant-core/config/{unlock-secret,sim-pin}`（0600，root）；不进 argv、不进 API 请求/响应、不进日志、不进审计；状态查询只返回“存在/不存在”。
- `adbhome/.android/adbkey`（0600）是敏感材料：不打印、不提交、不复制。
- 高风险动作（清数据、刷机、改分区、relock BL、覆盖 boot/vbmeta、删设备侧备份、`pm uninstall` 预装应用）必须单独获得用户明确确认。
- 锁屏造成的“不可验证”必须如实 skip，不得报成 FAIL；反过来也不得把“锁屏所以没验”说成通过。
- 装机顺序：刷模块 zip → 重启一次。不再有“root 授权要重启”这一步（D21 已删除 allowlist 授予层）。
- **重启不是默认手段**：只有验证启动路径本身（`service.sh` / `post-fs-data.sh` / 干净装机）才需要重启一次。守护侧改动（Go 代码、helper jar、APK、配置、WebUI）用 `tools/scripts/activate-device.sh`：停守护 → `ksud module install` 落盘 → `cp -a` 覆盖模块树 → 用与 `service.sh` 相同的方式起守护 → 等 health。

### `tooling/static-gate`
applies_when: "任何改动落地前后"
authority: "低于 docs/DESIGN.md，高于普通建议"
verification: "tools/scripts/run-all-static.sh"

- 一条命令跑完整静态门禁，它同时是“本轮没有破坏别的东西”的唯一凭据。跑完贴末尾结论，不要只贴一行 OK。
- 规模上限：≤ 60 项。静态层只覆盖“编译与契约”：`gofmt` / `go vet` / `go build`、`sh -n`、跨产物契约、APK 可构建且无 root 残留、文档索引。能力正确性以真机验收为准，不要用静态项数替代真机证据。
- 新增可被破坏的约束时，把断言加到 `check-contracts.sh`（跨产物）/ `check-docs.sh`（文档漂移）/ 对应 Go 单测，而不是靠人记得。
- 回归测试要双向断言：既断言“该生效时生效”，也断言“不该生效时不生效”。真机故障的回归先断言旧写法确实失败。
- 测试夹具必须来自真实设备输出，不能凭想象构造。`apps/pilot/build/keystore/`、`out/`、`modules/*.zip`、`apps/*/dist/` 都是产物，不得入库；若新增设备夹具，放在 `modules/sextant-core/tests/` 下并纳入 Git 跟踪（当前该目录只有 `.gitignore` 与遗留 `tests/bin/allowlist-tool`，没有 fakedev 夹具）。
- 当前工作区没有 `installer-test.sh`；若将来恢复 KernelSU 安装器测试，不得放宽任何沙箱守卫，且必须把测试写入限制在沙箱目录。
- warning、lint warning、格式漂移、生成物漂移、文档索引缺失和本轮 TODO/TBD 都按缺陷处理；除非明确记录为非本轮债务，否则不能带着它们声称完成。

### `host/local-build`
applies_when: "在本机（<host-ip>，/root/chopin-ksu）跑构建、验收或查阅外部研究资料"
authority: "低于 docs/DESIGN.md，高于普通建议"
verification: "nproc；free -g；df -h /；git status；sha256sum"

- 本机 Linux x64、6 vCPU、12GiB RAM；工作区固定在 `/root/chopin-ksu`，外部研究资料固定在 `/root/chopin-research/`，不要往 `/tmp` 或根目录散落大文件。
- 这台机器做不了全量 AOSP/LineageOS 构建，不要声称能。
- 构建与验收前先量资源：可用内存、1 分钟负载、`/root` 剩余空间；不满足就等或停。阈值以 `free`/`df`/`/proc/loadavg` 实测为准，先量后跑、超限即停。
- 外部下载、解包和反编译输入视为不可信数据；不执行下载来的二进制。

### `research/external`
applies_when: "需要查反编译、ROM、移植或旧构建证据"
authority: "只作参考，低于所有当前真源"
verification: "注明来源路径与日期；区分静态观察与运行时事实"

- `/root/chopin-research/` 是外部资料根目录：`decompiled/`、`downloads/`、`extract/`、`old/`、`research/`、`audit/`、`tools/`。
- 反编译是静态观察，不代表运行时行为；不把推测写成事实。
- 不把外部资料搬回本工作区；需要引用时在文档里写清路径。
- 分析第三方安全产品只做兼容性/可观测性研究，不绕过、不伪造、不分发密钥材料。

## 13. 网络加速与依赖源

以下地址于 2026-11-16 在本机实测可用；2026-11-19 增补 GitHub 备用入口。安装、克隆、下载前先用这里的源；换源与失败处理按 §2-10、§2-6。

### GitHub 资源加速（不止用于 clone）

**加速入口**（按优先级）

| 地址 | 用途 |
|---|---|
| `https://git.yylx.win/` | 主力。支持 `github.com`、`raw.githubusercontent.com`、`gist.githubusercontent.com`，可代理 clone、raw、archive、Release 资产、Gist |
| `https://gh.inkchills.cn/` | 备用加速 |
| `https://github.akams.cn/` | 加速站汇总目录，前两个不可用时来这里找替代 |

**统一拼接规则**

- 首选拼接式：在完整原始 URL 前加 `https://git.yylx.win/`；也可以把原域名替换成该域名，两种都可用，优先拼接。
- 下载失败时不要直接放弃、也不要转人工另找站点：先按上表顺序换入口、按规定写法重试，不走官方直连；实在不行再自行检索可用站点。

**常用写法**

```bash
# 克隆
git clone https://git.yylx.win/https://github.com/<owner>/<repo>.git

# 私有仓库（HTTPS）
git clone https://<user>:<token>@git.yylx.win/https://github.com/<owner>/<repo>.git

# raw 文件
curl -LO https://git.yylx.win/https://raw.githubusercontent.com/<owner>/<repo>/<branch>/<file>

# 分支归档
https://git.yylx.win/https://github.com/<owner>/<repo>/archive/refs/heads/<branch>.zip

# Release 资产
https://git.yylx.win/https://github.com/<owner>/<repo>/releases/download/<tag>/<file>

# Gist
https://git.yylx.win/https://gist.githubusercontent.com/<user>/<gist_id>/raw/<file>
```

- 不支持 SSH 克隆；只支持 HTTPS。
- 实测：`ls-remote`、raw 文件、archive 可用；`/releases/latest` 这类页面返回 400，只代理具体文件。
- 本机配了 `127.0.0.1` 代理而代理未启动时，git 会报连不上，这不是加速地址失效；先加 `-c http.proxy= -c https.proxy=` 再执行。

### Docker / OCI 镜像

- 前缀拉取：`docker pull git.yylx.win/<registry>/<image>:<tag>`，省略 registry 时按 `docker.io` 处理。
- 支持 `docker.io`、`ghcr.io`、`gcr.io`、`registry.k8s.io`、`quay.io`、`mcr.microsoft.com`、`docker.elastic.co`、`nvcr.io`。
- 也可在 Docker 守护进程配置 `registry-mirrors` 与 `insecure-registries` 指向该域名。
- 实测 `/v2/` 与 `docker.io/library/alpine` manifest 均返回 200。

### Python（pip / uv）

按顺序使用，括号内为实测响应时间：

1. `https://mirrors.aliyun.com/pypi/simple/`（0.20s）
2. `https://mirrors.cloud.tencent.com/pypi/simple/`（0.27s）
3. `https://repo.huaweicloud.com/repository/pypi/simple/`（0.34s）
4. `https://pypi.tuna.tsinghua.edu.cn/simple/`（2.87s）

- pip：`pip install -i <源> <pkg>`
- uv：`uv pip install --index-url <源> <pkg>`，或设置 `UV_DEFAULT_INDEX=<源>`
- 换源只作为命令参数或用户级配置，不写进项目依赖文件。

### JavaScript（npm / pnpm / yarn）

按顺序使用：

1. `https://registry.npmmirror.com`
2. `https://mirrors.cloud.tencent.com/npm/`
3. `https://repo.huaweicloud.com/repository/npm/`

- npm：`npm install --registry=<源>`
- pnpm：`pnpm install --registry <源>`
- yarn：`yarn install --registry <源>`

## 14. 真源优先级

1. `docs/DESIGN.md`（唯一设计真源）+ `docs/ACCEPTANCE.md`（验收流程）。
2. 当前源码、脚本、配置、单测夹具、运行日志、实机证据、当前 git 状态。
3. 本文件 `AGENTS.md`。
4. `docs/TASKS.md`、`README.md`、`WORKSPACE.md`、`modules/*/README.md`、`apps/*/README.md`。
5. `/root/chopin-research/`、旧会话、旧记忆：只作模式证据与历史追溯，不能覆盖当前事实。

冲突时先报冲突、证据与推荐处理方式，等确认。

## 15. 交接规则

当上下文过大、需要换窗口、需要交给另一个 agent，或任务尚未闭合时，必须产出可直接复制的交接文本，包含：

1. 当前目标和已确认的产品/架构边界。
2. 当前 git 状态、已改文件、未提交文件和嵌套仓库状态；HEAD 以 `git log -1` 为准，不写死。
3. 已完成工作、实际验收命令和结果。
4. 未闭合风险、漂移警告和禁止触碰的用户改动。
5. 下一步最安全命令和停止条件。
6. 未闭合的设计修订提案、等待用户确认的问题，以及确认后重新进入实现的最安全入口。

---

任何时候使用中文，除非用户明确要求英文输出。
