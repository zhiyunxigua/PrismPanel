# PrismPanel mod 统一管理与配置同步（服主/管理员指南）

> 适用于 PrismPanel v0.2.x：镜像服务器组支持 Fabric/Forge 模组服的 **config 统一同步**、**mod 统一管理** 与 **运行态上报**。

## 一、平台支持

在「服务器」→ 新建/编辑服务器组时选择平台：

| 平台 | 说明 |
|---|---|
| `paper` / `spigot` | Bukkit 生态（默认） |
| `fabric` / `forge` | 模组服（本改造新增） |
| `velocity` / `bungee` | 代理服（不支持镜像组） |

镜像服务器组仅支持 paper/spigot/fabric/forge。平台决定：

- 制品安装目录：`ArtifactDirectory` = `mods/`（fabric/forge）或 `plugins/`（paper/spigot）
- 配置同步默认目录：fabric/forge → `["config","plugins"]`；paper/spigot → `["plugins"]`
- 描述符解析：fabric → `fabric.mod.json`；forge → `META-INF/mods.toml`（或文件名回退）

## 二、mod config 统一同步

镜像组的「同步配置」按钮把镜像源的配置目录**批量覆盖**到各派生实例，用于统一管理模组配置文件（`config/*.toml`、`*.json`、`*.properties` 等）。

### 配置字段（镜像组 JSON `servers/<server_id>.json`）

```json
{
  "type": "mirror",
  "platform": "fabric",
  "config_sync_directories": ["config", "plugins"],
  "plugin_config_sync_extensions": [".yml", ".yaml", ".json", ".toml", ".ini", ".conf", ".properties", ".xml"]
}
```

- `config_sync_directories`：相对工作目录的同步根列表（相对路径，拒绝 `..`/绝对路径；支持嵌套如 `mods/config`）
- 不填时按平台取默认（旧配置自动向后兼容）
- `plugin_config_sync_extensions`：后缀白名单，只同步白名单内的普通文件

### 行为规则

- 同步目标语义：**排除项保留实例原有内容；其余以镜像源为准**（先复制到临时目录、再恢复排除项、最后原子交换）
- `plugins` 根跳过根级 jar（那是插件/模组文件，由 mod/插件管理处理）；`config` 等根允许根级白名单散文件
- 同步不停止/不重启实例；结束后需手动在控制台执行模组重载命令
- 每个同步根独立扫描/复制/进度；某根失败（如镜像源缺 `config` 目录）任务整体失败并带目录名报错

### 操作

1. 镜像源目录（`<root>/<image_directory>/`）内准备 `config/`（或其它同步根）内容
2. 服务器详情 → 「同步配置」→ 确认目录清单 → 执行
3. 部署任务弹窗查看进度与日志（stage：`scanning_config` / `copying_config`）

### 自适应检测与放置（v0.2.x）

「同步配置」前会自动**检测镜像源中的配置目录**并据此放置，针对插件与 mod 自适应：

- **插件服**（paper/spigot）：检测/同步 `plugins/`
- **mod 服**（fabric/forge）：检测/同步 `config/`（mod 配置位置）+ `plugins/`
- 自定义 `config_sync_directories` 时以其为准，叠加实际存在性检测

无法确定或目录缺失时前端**弹窗说明**：

| 场景 | 弹窗行为 |
|---|---|
| 镜像源无任何可同步目录（`NO_CONFIG_DIR_FOUND`） | 说明情况并**阻止同步**，提示创建 plugins/ 或 config/ |
| mod 服缺 `config/`（`MOD_CONFIG_DIR_MISSING`） | 说明"mod 配置通常在 config/ 下"，让用户决定是否仅同步 plugins/ |
| 插件服缺 `plugins/`（`PLUGINS_DIR_MISSING`） | 说明插件配置无法同步，让用户决定 |

安全：显式同步目录经 daemon 校验（拒绝绝对路径/`..` 逃逸，且必须属于该服务器允许的配置目录集合），防路径逃逸。

## 三、mod 统一管理

mod 管理复用插件体系，入口在「插件」页（平台筛选显示"Fabric 模组"/"Forge 模组"）与服务器详情的插件/模组区域。

### 支持的操作

| 操作 | 说明 |
|---|---|
| 上传/发布 | panel 仓库创建 fabric/forge 类型制品（`ValidPluginType` 含 fabric/forge） |
| 部署 | `/api/v1/plugins/{type}/{plugin}/{artifact}/deploy` 按平台过滤目标；fabric 制品不会装进 spigot 服（daemon `Deploy` 校验 `PluginType == PluginTypeForPlatform`） |
| 启用/禁用 | `.jar ↔ .jar.disabled` 重命名（Fabric/Forge 加载器约定），目标已存在时拒绝 |
| 卸载/删除 | 移除 jar（含 `.disabled`） |
| 漂移检测 | 启动前基线哈希 + 运行中扫描；状态：`loaded` / `not_loaded` / `version_mismatch` / `disabled_pending_restart` / `file_changed_since_start` |

### daemon 管理命令（面板内部使用）

```
mods.list / mods.enable / mods.disable / mods.uninstall
```

### 运行态上报（Fabric，v0.2.0）

> 设计详见 [mod运行态上报设计.md](./mod运行态上报设计.md)

将 `prism-fabric-0.2.0.jar` 放入 Fabric 子服 `mods/` 目录。daemon 启动实例时注入 `PRISM_DAEMON_WS` / `PRISM_INSTANCE_ID` / `PRISM_SESSION_ID` / `PRISM_PLUGIN_TOKEN` 环境变量，mod 通过 `FabricLoader.getAllMods()` 采集已加载 mod（只报 `PATH` 来源、过滤内置 id 与自身）并连接 daemon `/api/v1/ws/plugin` 上报：

- `mods.list` 将运行态与文件态合并，产生 `loaded` / `not_loaded` / `version_mismatch` / `pending_restart` 等精确状态（期望 vs 实际漂移检测）
- 10s 心跳 / 5s snapshot / 实例重启代次失效（旧连接自动失效）
- 环境变量缺失（如客户端误装）时静默禁用，不影响游戏

### Fabric mod 构建（开发者）

```bat
cd prism-plugin
:: 需要 JDK（本机用 JDK 25 验证；Gradle 8.10.2 守护进程须跑在 JDK 21 或更低）
:: 若本机无 JDK 17 工具链，临时将 build.gradle.kts 的 toolchain 17 改为 25（字节码仍 release 17）
set JAVA_HOME=C:\Program Files\Java\jdk-21.0.11
gradlew.bat :fabric:shadowJar --no-daemon --no-watch-fs
:: 产物：fabric/build/libs/prism-fabric-0.2.0.jar（357KB，gson 已 relocate，无 Loom、跨 MC 版本通用）
```

> 注：`gradle-wrapper.jar` 已从同仓库其它 Gradle 项目恢复；如首次构建需联网下载 Gradle 8.10.2 发行版与依赖。

## 四、常见问题

| 现象 | 处理 |
|---|---|
| 同步配置报 `INVALID_CONFIG` | 错误消息含同步根目录名；检查镜像源对应目录是否存在、是否在 `config_sync_directories` 中 |
| mod 启停后需重启生效 | `.disabled` 重命名不会热卸载；重启实例后生效 |
| `mods.list` 无运行态状态 | 未安装 `prism-fabric` mod、或实例在装有 mod 前已启动（需重启实例获得运行态上报） |
| Fabric 加载器版本 | `fabric.mod.json` 依赖 `fabricloader >= 0.14.0`；Fabric Loader 0.15+ 要求 Java 17 |
