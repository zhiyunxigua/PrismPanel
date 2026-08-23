# PrismPanel mod 运行态上报设计

**文档状态**：设计稿（待 t4 实施确认）
**适用阶段**：P3 阶段一——Fabric/Forge 服务端"实际加载了哪些 mod"的运行态上报方案
**更新日期**：2026-07-18
**关联文档**：[代理服与插件多平台设计.md](代理服与插件多平台设计.md)、[守护进程设计.md](守护进程设计.md)

---

## 一、目标与边界

### 1.1 目标

- daemon 能获知 Fabric 服务端**实际加载**的 mod 列表（mod id / 版本 / 来源 jar 文件名），与文件态（mods/ 目录扫描）合并，产生运行态确认的 mod 状态（loaded / not_loaded / version_mismatch / disabled_pending_restart 等）。
- 复用现有插件上报通道（`/api/v1/ws/plugin`）与信任模型（loopback + token/session/pid 校验），不新增监听端口。
- 提供明确的"最小可用闭环"实施路径，供 t4 落地。

### 1.2 不做的功能（v1）

- 不实现 Forge 侧上报（见第五章评估，延后）。
- 不做 mod 运行态控制（热启用/热禁用）——Fabric 无标准热装载 API，仅做**观测**。
- 不自动把 prism-fabric-mod 注入每个服务端（与 Spigot 插件现状对称：用户手工放置或经部署管线分发）。
- 不改变现有 mods.list 返回结构与前端协议（只做小字段增强，见 7.4）。

---

## 二、现状梳理（关键代码路径）

| 关注点 | 位置 | 现状 |
|---|---|---|
| 运行态上报协议 | `daemon/internal/api/plugin.go` | `/api/v1/ws/plugin`：`auth` → `auth.result`（protocol_version "2"、sample_interval_seconds 5）→ `heartbeat` / `snapshot` / `response` / `operator.drift`；读超时 35s |
| 连接注册与代次失效 | `daemon/internal/supervisor/plugin.go` | `RegisterPlugin`：token 哈希比对 + sessionID + `processBelongsToTree(pid)` + platform 匹配（`PluginTypeForPlatform(cfg.Platform)`）；每次启动 `pluginGeneration++`，旧连接失效 |
| 上报数据模型 | `daemon/internal/supervisor/types.go` | `PluginReport`（TPS/MSPT/在线人数/JVM/Players/`Plugins []LoadedPlugin`）；`LoadedPlugin{name,version,main,authors,enabled,source_file}` |
| 文件态与运行态合并 | `daemon/internal/plugins/service.go` `merge()` | 已按实例平台分流：mod 平台走 `scanMods()` + **同一 merge()**（`snapshot.Plugins` 作运行态输入），状态机完整（见 7.3） |
| mod 文件态描述符 | `daemon/internal/plugins/descriptor_platform.go` | `parseFabricJAR`（fabric.mod.json）、`parseForgeJAR`（META-INF/mods.toml → 文件名回退） |
| 环境变量注入 | `daemon/internal/supervisor/lifecycle.go` `prepareCommand` | 对每个实例进程注入 `PRISM_DAEMON_WS` / `PRISM_INSTANCE_ID` / `PRISM_SESSION_ID` / `PRISM_PLUGIN_TOKEN` |
| Spigot 上报客户端 | `prism-plugin/core/DaemonBridge.java` + `spigot/PrismSpigot.java` | `PrismEnvironment.fromSystem()` 读环境变量；连接 → 认证 → 10s 心跳 + 5s snapshot；断线指数退避重连 |
| mods.list 出口 | `daemon/internal/api/server.go` | `mods.list` / `plugin.list` 共用 `plugins.Service.List()`，返回 `ListResult{kind:"mod", plugin_connected, pending_restart, items}` |
| 前端状态文案 | `frontend/src/views/ServerDetailView.vue` | `pluginStatusLabels` 已覆盖全部合并状态（"已装载/未装载/版本不符/禁用待重启…"） |

**关键结论**：daemon 侧 `merge()` **已经**把 `snapshot.Plugins` 当作运行态输入与 mods/ 文件态合并——Spigot 插件上报的 `plugins` 数组语义为"已加载制品"。因此 Fabric 侧只要把"已加载 mod 列表"放进同一个 snapshot 的 `plugins` 数组，**mods.list 的运行态合并即开箱可用**。v1 不需要新增消息类型（见第六章取舍）。

---

## 三、推荐方案总览（决策摘要）

1. **上报载体**：新建 Fabric mod（`prism-fabric-mod`），用 Fabric Loader API（`FabricLoader.getAllMods()`）采集已加载 mod，连接 daemon 注入的 WebSocket 上报；**第一版只做 Fabric，Forge 延后**。
2. **协议**：复用 `/api/v1/ws/plugin` 与 `snapshot` 消息，mod 列表放在 `plugins` 数组（实例平台即语义分界）；capabilities 新增 `mod.inventory`（信息性）；`LoadedPlugin` 增加可选 `id` 字段（硬化匹配）；心跳/代次失效机制**直接复用**。
3. **daemon 合并**：`merge()` 零改动即可工作；附加小改动：`PluginReport` 玩家数字段指针化（Fabric 侧无 MC 类无法取在线人数，避免假 0）、`Descriptor/FilePlugin` 增加 `id`（按 id 优先匹配 + 过滤 prism-fabric 自身）、mods.list 结构不变。
4. **部署形态**：`prism-plugin/fabric/` 新子模块（`:fabric`），依赖 `:core` 复用 DaemonBridge/PrismCore；**不引入 Fabric Loom**（只用 fabric-loader API，不引用 MC 类，普通 Gradle + shadow 打包 gson）。
5. **工作量**：约 3 个实施步骤可闭环（见第十章），daemon 改动小而低风险。

---

## 四、Fabric 侧上报载体（prism-fabric-mod）

### 4.1 为什么是一个 Fabric mod

Fabric 服务端没有 Spigot 插件 API，无法注入插件；要读取加载器内部状态（已加载 mod 列表），唯一受支持且稳定的入口就是 **Fabric Loader 公开 API**，因此必须以一个 Fabric mod 的身份运行在服务端 JVM 内（`mods/` 目录），这与 Spigot 的 PrismMC 插件在服务端进程内工作是对称的。

### 4.2 采集 API（Fabric Loader，0.14+）

```
FabricLoader.getInstance().getAllMods()        // Collection<ModContainer>
ModContainer.getMetadata()                      // ModMetadata
    .getId()                                    // mod id（fabric.mod.json id）
    .getName()                                  // 显示名（缺省回退 id）
    .getVersion().getFriendlyString()           // 版本字符串（与 fabric.mod.json version 同源）
    .getAuthors()                               // List<Person>，取 getName()
ModContainer.getOrigin()                        // ModOrigin（loader 0.14+）
    .getKind()                                  // ModOrigin.Kind：DIRECT / NESTED / PATH
    .getPaths()                                 // List<Path>，取第一个文件名的 basename 作 source_file
```

**过滤规则（必须）**：
- 只上报 `getKind() == ModOrigin.Kind.DIRECT` 的 mod —— 即直接放在 `mods/` 的 jar。NESTED（fabric-api 的子模块、jar-in-jar 内嵌库）与 PATH/UNKNOWN（fabricloader、minecraft 等由启动器装载的）一律排除，保证**运行态列表与文件态扫描（mods/ 顶层）一一对应**，避免 fabric-api 一个 jar 上报十几个子模块造成版本误判。
- 防御性排除内置 id：`minecraft`、`fabricloader`、`java`（即使某 loader 版本把它们归为 DIRECT）。
- 排除自身 id `prism-fabric`（daemon 侧另有兜底过滤，见 7.2）。
- 已加载即 `enabled=true`（Fabric 无运行态禁用概念；`enabled=false` 只存在于 .jar.disabled 未加载场景，由文件态表达）。

### 4.3 上报载体实现（复用 prism-plugin core）

新增 `prism-plugin/fabric/` 子模块，复用 `:core` 的现成组件：

| 组件 | 复用 | 新增 |
|---|---|---|
| `PrismEnvironment.fromSystem(logger, "fabric")` | ✅ | — |
| `DaemonBridge`（连接/认证/心跳/重连/命令响应） | ✅ | — |
| `PrismCore.create(...)` 装配与 5s snapshot 调度 | ✅ | — |
| `PrismLogger` 实现 | — | `FabricLogger`（Fabric Loader 自带 slf4j 可用则包装，否则 `System.out` 打印） |
| `PlatformScheduler` 实现 | — | `FabricScheduler`（`ScheduledExecutorService` 极简适配；mod 不执行任何平台命令） |
| `TelemetryProvider` 实现 | — | `FabricTelemetry.snapshot()`：JVM 堆/线程（`Runtime`/`ManagementFactory`，无需 MC 类）+ `plugins` 数组（4.2 采集结果） |
| 入口 | — | `PrismFabric implements ModInitializer`：`onInitialize()` 读环境变量，缺失则日志后静默返回（等同 Spigot 的"环境不可用则禁用"） |

capabilities 声明：`telemetry` + `mod.inventory`（`mod.inventory` 为新增信息性能力，daemon 记录到 `plugin_capabilities` 供诊断；v1 不做强制 gate）。

**上报快照内容**（snapshot data）：

```json
{
  "jvm_heap_used_bytes": 123456789,
  "jvm_heap_max_bytes": 2147483648,
  "jvm_threads": 42,
  "plugins": [
    {"id": "sodium", "name": "Sodium", "version": "0.5.11+mc1.21.1",
     "authors": ["jellysquid3"], "enabled": true, "source_file": "sodium-fabric-0.5.11+mc1.21.1.jar"}
  ]
}
```

（不发送 TPS/MSPT/players——无 MC 类无法采集；配合 7.2 的指针化改动，UI 不会显示假数据。）

### 4.4 构建方式：不引入 Fabric Loom

**推荐无 Loom 方案**：本 mod 只依赖 fabric-loader 的公开 API（`net.fabricmc.loader.api.*` 与 `net.fabricmc.api.ModInitializer`），**完全不引用任何 Minecraft 类**，因此不需要 mappings/yarn 反混淆，无需 fabric-loom 插件：

- 普通 Gradle `java-library` 子模块（与现有 spigot/velocity 一致），`compileOnly("net.fabricmc:fabric-loader:0.16.x")`（仓库 maven.fabricmc.net）。
- gson 经 `com.gradleup.shadow`（构建已声明，8.3.6）打进 fat jar，并 **relocate** 到 `com.xigua.prism.shaded.gson` 避免与其它 mod 冲突。
- 产物：`prism-fabric-<version>.jar`，直接放入服务端 `mods/`。

对比 Loom 方案（fabric-loom + yarn mappings）：需要按 MC 版本/loader 版本锁定映射、下载 mappings、构建重；收益为零（我们不碰 MC 类）。**取舍理由：显著降低构建复杂度与 CI 依赖，且产物天然跨 MC 版本**（同一 jar 可用于 1.18~1.21+，只要 loader ≥0.15）。

### 4.5 fabric.mod.json 与生命周期

```json
{
  "schemaVersion": 1,
  "id": "prism-fabric",
  "version": "0.2.0",
  "name": "Prism Fabric",
  "description": "PrismPanel 运行态上报客户端",
  "environment": "*",
  "entrypoints": { "main": ["com.xigua.prism.fabric.PrismFabric"] }
}
```

- `environment: "*"`：不用 `"server"`（避免个别 loader 版本对侧限制行为不一致导致客户端崩溃），改为**运行时自禁用**：`onInitialize()` 检查 `PRISM_*` 四件套缺失即返回——客户端误装仅多一个不干活的小 mod，不崩游戏。
- 生命周期：随服务端 JVM 启停。daemon 每次启动重新注入新 session/token，mod 进程重启后自动重连（复用 DaemonBridge 退避重连）。
- Java 要求：服务端 Java ≥17（Fabric Loader 0.15+ 本身要求 Java 17，一致）；不支持 Java 8 旧版 MC（文档注明即可）。

---

## 五、Forge 可行性评估与结论

**可行性**：Forge 1.13+ 提供运行态 mod 列表 API：

```
net.minecraftforge.fml.ModList.get().getMods()      // List<ModInfo>
ModInfo.getModId() / getVersion() / getDisplayName()
ModInfo.getOwningFile() → ModFileInfo.getFile() → ModFile.getFilePath()  // jar 路径
```

NeoForge 有对等 API（`net.neoforged.fml.loading.moddiscovery.ModInfo`）。技术上可上报 id/version/source_file，与 daemon 契约完全兼容（协议按加载器无关设计）。

**成本**：
- 无 Loom-free 路径：Forge 没有"纯 loader API 不碰 MC 类"的写法（Forge mod 必须经 ForgeGradle 按 MC 版本编译，需 mappings）。
- 版本碎片化：1.12.2（无 ModList，需 coremod/tweaker）、1.16.5、1.18.2、1.19.x、1.20.x、1.21.x API 各不相同，一套构建无法覆盖。

**结论（推荐）**：**第一版只做 Fabric，Forge 延后**。理由：Fabric 用最少的构建成本即可闭环，且能验证协议与合并逻辑的正确性；Forge 侧协议零改动，后续以独立 ForgeGradle 工程补上（列为 v1.5/二期，优先级低于本 P3 其它项）。NeoForge 可并入二期一起评估。

---

## 六、协议设计

### 6.1 复用 vs 新增消息：决策

| 方案 | 描述 | 优点 | 缺点 |
|---|---|---|---|
| **A（推荐）** | 复用 `snapshot`，mod 列表放 `plugins` 数组；`LoadedPlugin` 加可选 `id` | merge() 零改动即闭环；心跳/代次/重连全继承；改动最小、风险最低 | 语义上"plugins 字段装 mods"依赖实例平台分界；无法携带 loader 级元数据（v2 需求） |
| B | `LoadedPlugin` 加 `kind=mod` 字段 | 字段级区分 | 需要 daemon 按 kind 分拣、迁移既有上报客户端；收益低（实例平台已决定语义） |
| C | 新消息类型 `mods.snapshot` + `ModReport{loader, mods}` | 语义最清晰，可带 loader 版本等 | 新增一整套存储/合并/校验路径，工作量翻倍；v1 无此需求 |

**取舍理由**：实例平台（fabric/forge）已经唯一决定 `plugins` 数组的语义是"已加载 mod"，方案 B 的区分是冗余的；方案 C 的 loader 级元数据（loader 版本、游戏版本）属于未来漂移诊断增强，协议已预留升级路径（6.5）。**v1 采用方案 A，新增 `mod.inventory` capability 作为显式能力声明**，让 daemon/UI 能区分"Spigot 插件上报"与"Fabric mod 上报"。

### 6.2 消息与字段

- `auth`：`platform: "fabric"`（通过 `PluginTypeForPlatform` 校验）、`capabilities: ["telemetry","mod.inventory"]`。
- `snapshot`：data 中 `plugins` 数组元素 = 4.2 的采集结果。
- `LoadedPlugin`（daemon 侧）新增：`ID string \`json:"id,omitempty"\`` —— v1 可选，merge 优先按 id 匹配（见 7.2），同时解决同名 mod 与 prism-fabric 过滤。
- `auth.result`：`protocol_version` 保持 `"2"`；仅当未来引入 `mods.snapshot` 消息时升 `"3"`。

### 6.3 上报频率与心跳

**直接复用现有节奏，不做新设计**：

| 项 | 值 | 依据 |
|---|---|---|
| 心跳 | 每 10s | DaemonBridge `sendHeartbeat`；daemon 读超时 35s，余量充足 |
| snapshot | 每 5s | PrismCore `telemetryTask`（5s 初值/周期）；与 auth.result 的 `sample_interval_seconds: 5` 一致 |
| 数据量 | 数百 mod ≈ 数十 KB | daemon 读限制 1MB、`Update()` 校验 `len(Plugins) > 5000`，均远未触及 |

### 6.4 实例重启代次失效

**直接复用，零改动**：每次实例启动，daemon 生成新 `sessionID` + 新 `pluginToken`（`randomPluginToken`）、`pluginGeneration++`、`pluginTokenHash` 重置；旧连接的任何操作都会命中 `sessionID/generation` 不匹配而失效。Fabric mod 随 JVM 重启以新进程新环境变量重新认证，天然纳入同一机制。

### 6.5 升级路径（v2 预留，不实现）

若未来需要 loader 级漂移诊断（如 loader 版本与 daemon 期望不符、游戏版本漂移），新增消息 `mods.snapshot`：

```json
{"type": "mods.snapshot", "data": {
  "loader": {"type": "fabric", "loader_version": "0.16.9", "game_version": "1.21.4"},
  "mods": [ {"id":"sodium", "version":"0.5.11+mc1.21.1", "source_file":"sodium-fabric-0.5.11+mc1.21.1.jar"} ]
}}
```

daemon 侧 `supervisor` 增 `modReport` 字段 + `api/plugin.go` 增消息分支，`plugins.Service.List()` 优先消费 modReport；协议版本升 `"3"`，旧客户端不受影响。**v1 明确不做，仅此留档**。

---

## 七、daemon 合并与漂移

### 7.1 merge() 现成能力（零改动部分）

`plugins/service.go` 的 `merge(files, runtime, connected, changes)` 对 mod 平台**已经可用**：

- 匹配：`byFile[basename(source_file)]` 优先，`byName` 兜底——Fabric 文件态与运行态都读同一份 fabric.mod.json，name/version 同源，天然吻合。
- 状态推导（connected=true 时）：见 7.3 状态表。
- 漂移：`beforeStart` 哈希基线 + 运行中 5s 轮询 `scanRunningInstances` → `file_changed_since_start`，与运行态版本比对互补（文件变了但运行态还是旧版 → `version_mismatch`；文件变了运行态也新 → 仅 pending）。

### 7.2 需要的小改动（daemon，低风险）

1. **`PluginReport.OnlinePlayers/MaxPlayers` 改指针**（`*int` + `omitempty`）：Fabric mod 无 MC 类取不到在线人数，若保持 int 会在实例快照里显示假 `0/0`；指针化后未上报即 nil，UI 自动隐藏（与当前无插件连接行为一致）。Spigot 上报数值不受影响（JSON 兼容）。`Update()` 校验相应改为 nil 安全。
2. **`Descriptor`/`FilePlugin` 增加 `id` 字段**：`parseFabricJAR` 填 `raw.ID`、`parseForgeModsTOML` 填 `modId`（velocity 已有 id；spigot/bungee 无 id 时以 name 兜底）。用途：merge 按 id 优先匹配（运行态 `LoadedPlugin.id` ↔ 文件态 id），更稳。
3. **mod 平台过滤 prism-fabric 自身**：`plugins.Service.List()` 对 `kind=="mod"` 的结果过滤 `item.ID == "prism-fabric"`（常量 `modReporterID`），避免用户误操作禁用上报组件；同时 merge 内对该项跳过。运行时上报侧也自排除（双保险）。
4. **（可选）基线扫描排除 prism-fabric**：`scanEnabledHashes` 按文件名前缀排除，避免升级 prism-fabric 后实例整体误标 pending_restart（不改也不影响正确性，仅提示噪音）。

### 7.3 状态机（mod 平台，connected=true）

| 文件态 | 运行态 | 状态 | issues / 备注 |
|---|---|---|---|
| jar 存在、enabled | 已加载，版本一致 | `loaded` | — |
| jar 存在、enabled | 未加载 | `not_loaded` | 装载失败（缺依赖/不兼容），无 pending |
| jar 存在、enabled，版本 != 运行态 | 已加载旧版 | `update_pending_restart` | `version_mismatch`，pending |
| jar 存在、enabled，运行中被替换 | 已加载旧版 | `update_pending_restart` | `file_changed_since_start`（+version_mismatch） |
| jar 存在、disabled（.jar.disabled） | 已加载（重启前旧态） | `disabled_pending_restart` | `disabled_file_still_loaded`，pending |
| jar 存在、disabled（.jar.disabled） | 未加载 | `disabled` | — |
| jar 缺失（运行中被删） | 已加载 | `uninstall_pending_restart` | `runtime_plugin_file_missing`，pending |
| jar 缺失 | 已加载但无 source_file | `runtime_only` | `runtime_plugin_source_unavailable`（fabric 侧不会出现，防御） |
| 同名冲突 | 任意 | `conflict` | `duplicate_plugin_name` |

未连接（`plugin_connected=false`，即无 prism-fabric-mod 或未连上）时回退现状：`file_enabled` / `file_disabled`，无运行态判定——**向后兼容，老部署不受影响**。

### 7.4 mods.list 返回结构

**保持现有 `ListResult` 结构不变**（`kind:"mod"`、`plugin_connected`、`pending_restart`、`items`、`warnings`），字段已足够表达运行态合并结果；前端 `pluginStatusLabels` 已覆盖全部状态文案。可选 UI 微调（不阻塞实施）：
- `ServerDetailView.vue` 的 "Prism 插件 已连接" → mod 平台显示 "Prism mod 已连接"；
- "内置插件"（runtime_only）文案 → "系统组件"。

---

## 八、部署形态

### 8.1 目录与模块

- 新建 **`prism-plugin/fabric/`** 子模块（Gradle 模块名 `:fabric`），加入 `prism-plugin/settings.gradle.kts`。
- **不新建独立仓库**：与 core/spigot/velocity/bungee 同构，最大化复用（DaemonBridge/PrismEnvironment/PrismCore 直接依赖 `:core`）。
- 新增源文件：`fabric/src/main/java/com/xigua/prism/fabric/{PrismFabric,FabricLogger,FabricScheduler,FabricTelemetry}.java` + `fabric/src/main/resources/fabric.mod.json`。

### 8.2 构建与产物

- 子模块 `build.gradle.kts`：`java-library` + `compileOnly("net.fabricmc:fabric-loader:0.16.x")` + shadow（relocate gson），产物 `prism-fabric-<version>.jar`。
- 构建入口：`prism-plugin/gradlew :fabric:shadowJar`（构建脚本 `build-all.bat` 视需要补充）。

### 8.3 分发到服务器

与 Spigot 插件完全对称的两条路径：
1. **手工**：把 jar 放入服务端 `mods/`（README 说明）。
2. **面板部署**：jar 作为 fabric 制品上传插件仓库（`parseJAR` 识别 fabric.mod.json），经现有 `/api/v1/plugins/deploy` 管线分发（`ArtifactDirectory(mod 平台) == "mods"`，deploy 已支持 fabric 平台，含镜像服 image+instances 全目标）。

v1 **不做** daemon 自动注入（与 Spigot 插件现状一致；环境变量对所有子进程注入，mod 在即上报，不在即仅文件态，无耦合）。

### 8.4 与 daemon 版本兼容策略

- 协议零变更 → **双向兼容**：新 daemon + 旧 mod、旧 daemon + 新 mod 均正常工作（新 mod 用旧 daemon 时 `mod.inventory` capability 被忽略，其余照常）。
- `LoadedPlugin.id` 为可选字段：旧 daemon 忽略，新 daemon 读不到时回退 name 匹配。
- 版本策略：mod 版本与 prism-plugin 主版本（0.2.0）同步发布；`protocol_version` 仅在引入不兼容消息时递增。

---

## 九、工作量与风险

### 9.1 风险清单

| 风险 | 等级 | 缓解 |
|---|---|---|
| Fabric Loader API 跨版本差异（0.14→0.16） | 低 | 只用稳定 API（getAllMods/getOrigin）；以 0.16.x 编译，声明支持 0.15+ |
| gson 类加载冲突 | 低 | shadow relocate 到私有包 |
| 客户端误装 mod 崩溃 | 低 | `environment:"*"` + 环境变量自禁用 |
| fabric-api 子模块误报导致版本误判 | 低 | 4.2 的 DIRECT 过滤（唯一关键规则，实施时重点测试） |
| 老 Java（MC <1.17）无法使用 | 低 | 文档说明；Fabric 生态本身已要求 Java 17 |
| daemon 假显示在线人数 0 | 中 | 7.2-1 指针化（必须做，否则 fabric 实例 UI 出现 0/0） |
| 运行态与文件态名字不匹配（显示名 vs id） | 低 | 同源元数据 + 7.2-2 按 id 匹配硬化 |

### 9.2 工作量估算（相对）

| 步骤 | 内容 | 量级 |
|---|---|---|
| S1 | `:fabric` 模块骨架 + 采集 + 上报 + shadow 构建 | ~1 人日 |
| S2 | daemon 小改：指针化 + id 字段 + 过滤 + 单测 | ~0.5 人日 |
| S3 | 本地 Fabric 服联调 + 状态矩阵验证 + 文档收尾 | ~0.5 人日 |
| （延后） | Forge mod / v2 mods.snapshot | 各 ≥2 人日，不排入 v1 |

---

## 十、实施步骤拆分（供 t4）

1. **S1 – Fabric mod 骨架**（纯新增，不碰 daemon）：
   - `prism-plugin/settings.gradle.kts` 增加 `:fabric`；写 `fabric/build.gradle.kts`（loader compileOnly + shadow + relocate gson）、`fabric.mod.json`、`PrismFabric/FabricLogger/FabricScheduler/FabricTelemetry`。
   - `FabricTelemetry.snapshot()` 实现 4.2 采集 + 过滤（DIRECT + id 黑名单 + 自排除）；`plugins` 数组元素含 `id/name/version/authors/enabled/source_file`。
   - 产物 `prism-fabric-0.2.0.jar` 手工放入本地 Fabric 测试服 `mods/`，启动后看 daemon 日志确认 auth 成功、`plugin_connected=true`。
2. **S2 – daemon 小改**：
   - `PluginReport.OnlinePlayers/MaxPlayers` → `*int` + `Update()` 校验适配 + 相关单测。
   - `Descriptor/FilePlugin` 增加 `id`；`parseFabricJAR`/`parseForgeModsTOML`/velocity 填充；`merge()` 增加 byID 匹配优先；`LoadedPlugin` 增加 `id`。
   - `plugins.Service.List()` mod 平台过滤 `modReporterID == "prism-fabric"`。
3. **S3 – 联调验证**（对照 7.3 状态矩阵）：
   - 正常运行 → `loaded`；mods/ 新增 jar 未重启 → `not_loaded`；替换 jar → `version_mismatch`+pending；运行中改 `.jar.disabled` → `disabled_pending_restart`；删除 jar → `uninstall_pending_restart`；无 mod 的旧部署 → 仅文件态（回归不坏）。
   - 回归：Spigot/Paper/Velocity/Bungee 的 plugin.list 行为不变；前端 ServerDetailView 文案可选微调。

---

## 附录：参考

- Fabric Loader API（javadoc）：<https://maven.fabricmc.net/docs/fabric-loader-0.15.2/net/fabricmc/loader/api/ModContainer.html>
- fabric.mod.json 规范：<https://wiki.fabricmc.net/documentation:fabric_mod_json>
- Fabric Loader 文档：<https://docs.fabricmc.net/develop/loader/>
- Forge ModList / ModInfo：<https://mcstreetguy.github.io/ForgeJavaDocs/1.19.4-45.1.0/net/minecraftforge/fml/ModList.html>
