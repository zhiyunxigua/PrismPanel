# WinApp 本地游戏启动设计（国际版）

> 本文描述 WinApp 侧「加入游戏」能力的当前设计与实现：面向用户本机启动 Java Minecraft（国际版）客户端，并连接到用户输入的服务器地址。
> 实现位于 `winapp/internal/game/mc_*.go`（认证/版本/下载/Fabric/Mod/启动），前端页面为「加入游戏」（`JoinGameView.vue`）。

## 背景

“加入游戏”能力面向用户本机启动一个 Java Minecraft 客户端，并连接到用户输入的服务器地址，能力必须放在 WinApp 侧实现，原因是：

- 启动 Java 进程必须发生在用户本机；
- Java 运行时、Minecraft 客户端文件、mods、config、resourcepacks、shaderpacks 都属于本地文件；
- 账号凭据（微软/第三方）属于用户本地敏感数据，不应进入 Panel 数据库；
- Panel Web 端运行在浏览器环境，不能可靠管理本地进程和本地文件。

本设计只做必要能力，不直接变成完整启动器（完整启动器能力参考 PCL2 源码研究报告 `docs/PCL2-winapp改进建议.md`）。

## 账号模式

支持三种账号模式（`MCAuthMode`）：

- **离线（offline）**：任意用户名 + 稳定 UUIDv3（按用户名生成），无需联网认证；
- **微软（microsoft）**：OAuth 设备码登录（`MCStartDeviceLogin` → `MCPollDeviceLogin`），登录后本地保存 access/refresh token，可静默刷新；
- **第三方（third_party）**：authlib-injector 兼容认证服务器（`MCThirdPartyLogin`），保存 `auth_server` 与 `client_token`。

账号本地保存于 WinApp 凭据存储，模式/用户名/过期时间可读（`MCAuthStatus`），支持退出登录（`MCLogout`）。

## 用户流程

1. 用户点击「加入游戏」（仅 WinApp 显示该入口）。
2. 设置账号（离线用户名 / 微软设备码 / 第三方服务器）。
3. 添加版本（可选）：从 Mojang 正式版本列表下载安装，或粘贴自定义版本 JSON 直链；Fabric 版本可另装 Loader。
4. 填写启动表单：服务器 IP、端口、版本、内存、实例目录、JVM 参数。
5. 点击「启动」：校验 → 选 Java → 核对版本文件与 natives → 拼接标准 Mojang 参数 → 启动 Java 进程 → 返回进程状态与日志入口。

## 目录设计

WinApp 管理自己的游戏缓存目录：

```text
%LocalAppData%/PrismPanel/game-cache/
  java/                          # Java 运行时（自动下载解压）
    jre-legacy/ alpha/ beta/ gamma/ delta/ epsilon/
  minecraft/
    {版本}/.minecraft/           # 每版本独立 .minecraft
      assets/
      libraries/
      versions/
      mods/
      config/
  downloads/                     # 下载中转（.part 完成改名复用）
  logs/
```

- 每版本独立 `.minecraft`（`minecraft/<版本>/.minecraft`），`PRISMPANEL_MC_DIR` 可覆盖根目录；
- 用户可指定**实例目录**：启动时合并 `mods/config/resourcepacks/shaderpacks`，用户内容优先覆盖同名文件，不反向写回用户目录；
- 启动结束不自动删除，提供手动清理能力。

## Java 管理

- 组件映射以 Mojang 官方数据为准：`jre-legacy=8`、`java-runtime-alpha=16`、`beta/gamma=17`、`delta=21`、`epsilon=25`，`>= 22` 前瞻到 epsilon；
- 启动前自动选 Java：版本 JSON 的 `javaVersion.majorVersion/component` 为准 → 系统扫描 → 自动下载缺失运行时（并发下载、下载后大版本校验）；
- 用户可在总设置或版本设置中显式指定 `java_path`。

## 版本管理

- 版本来源：Mojang `version_manifest_v2.json`（`MCAvailableVersions`）+ **自定义版本 JSON 直链**（http/https，前端「添加版本」粘贴）；
- 已安装版本（`MCInstalledVersions`）每版本独立目录，可删除（带重试）、可标记 Fabric；
- Fabric：`MCFabricLoaders(gameVersion)` 列出可用 Loader，`MCInstallFabric` 安装后生成 `fabric-loader-{loader}-{game}` 子版本，可在版本设置中 `UseFabric` 或显式 `launch_version`；
- 版本设置（`MCGetVersionSettings`/`MCSaveVersionSettings`）：服务器 IP/端口、内存、实例目录、JVM 参数、UseFabric/LaunchVersion、Java 路径、窗口宽高。

## 下载系统

- 多源镜像：默认镜像优先、官方兜底；官方源测速 `<4s` 才官方优先；镜像映射覆盖 root/maven/libraries/assets/fabric-meta；BMCLAPI `https://bmclapi2.bangbang93.com/`；
- 环境变量 `PRISMPANEL_MC_MIRROR`：`bmclapi`（强制镜像）/ `off`（关闭）/ 自定义 http 镜像地址；
- 完整性：sha1/size 校验（损坏自动重下）、429 退避、大文件 30min 长超时、取消不拉黑源；
- 下载队列（`MCDownloadList`/`MCCancelDownload`/`MCRemoveDownload`/`MCClearDownloads`/`MCAddDownload`），`prism:mc-download` 事件实时推送进度，最多 3 个版本并行。

## Mod 管理

- 每版本 `mods/` 目录：启停（`.jar ↔ .jar.disabled` 重命名）、删除、打开目录（`MCModsList`/`MCModsToggle`/`MCModsDelete`/`MCModsOpenDir`）；
- Modrinth 搜索/安装（`MCSearchModrinth`/`MCModrinthInstall`）：覆盖安装 Bug 修复 + size 校验，**依赖自动安装**（递归 required、去重防环、深度上限 5）。

## 启动参数

启动命令为标准 Mojang 客户端参数（非 `java -jar` 单命令），按版本 JSON 的 `arguments.jvm`/`arguments.game` 组装：

```text
--username {name} --uuid {uuid} --accessToken {token}
--version {version} --gameDir {gameDir} --assetsDir {assetsDir}
--assetsIndex {index} --userType mojang
--server {ip} --port {port}
--width {w} --height {h}  (可选)
```

要点：

- 版本 JSON 占位符（`${classpath}`/`${natives_directory}` 等）必须全部替换，缺失占位符 dropUnresolved 兜底；
- 启动参数去重（移植 PCL2 `DeduplicateJavaArguments`）；
- natives 启动前按“文件名+大小”完整核对并清版本残留；
- 中文用户名按 Java 大版本注入编码参数（UTF-16 原生传递）；
- QuickPlay 自动进服边界（23w31a 精确判断）；
- 启动命令日志脱敏（不输出 access token）。

## 启动流程与进度

- `MCLaunch(input)`：校验 → 选 Java → 准备版本 → 检查 natives → 拼接参数 → 启动进程；同一时刻仅允许一个游戏进程（`MCCloseGame(id)` 可关闭）；
- `MCLaunchProgress(id)` 返回 `game.JoinProgress`（percent/stage/message/running/error），前端轮询展示启动进度条；
- 进程环境变量 `APPDATA/appdata` 指向游戏目录；用 `java.exe`（非 javaw）便于日志捕获。

## API 与模块划分

### WinApp 后端模块（`winapp/internal/game/`）

| 文件 | 职责 |
|---|---|
| `mc_auth.go` / `mc_authlib.go` | 微软设备码 OAuth、离线账号、第三方 authlib-injector、token 存储 |
| `mc_version.go` | Mojang 版本清单、版本安装/删除、版本设置 |
| `mc_java.go` | Java 选择（组件映射/系统扫描/自动下载） |
| `mc_mirror.go` | 下载镜像、源健康记忆、测速 |
| `mc_fabric.go` | Fabric Loader 安装 |
| `mc_mods.go` | Mod 启停/删除/Modrinth 搜索安装/依赖安装 |
| `mc_launch.go` | 启动流程编排、参数组装、QuickPlay |
| `mc_queue.go` | 下载队列 |
| `mc_store.go` | 版本/设置本地存储（按平台） |
| `mc_dev.go` | 开发者模式日志 |

### WinApp 暴露给前端的方法（`runtime.js` 的 `mc*` 系列）

```text
账号:  MCAuthStatus / MCStartDeviceLogin / MCPollDeviceLogin / MCSetOfflineAccount / MCLogout / MCThirdPartyLogin
版本:  MCAvailableVersions / MCInstalledVersions / MCInstallVersion / MCDeleteVersion / MCGetVersionSettings / MCSaveVersionSettings
Fabric: MCFabricLoaders / MCIsFabricInstalled / MCInstallFabric
下载:  MCAddDownload / MCDownloadList / MCDownloadActiveCount / MCCancelDownload / MCRemoveDownload / MCClearDownloads
Mod:   MCModsList / MCModsToggle / MCModsDelete / MCModsOpenDir / MCSearchModrinth / MCModrinthInstall
启动:  MCLaunch / MCLaunchProgress / MCCloseGame
工具:  SelectMCGameDirectory / SelectJavaExecutable / MCGetLauncherSettings / MCSaveLauncherSettings / MCSetDevMode / MCDevModeEnabled / MCDevLogList / MCDevLogClear / MCOpenDevLog / MCDevLogPath
```

### 前端页面

- 入口：左侧导航「加入游戏」（`winAppOnly`）。
- 「加入游戏」页（`JoinGameView.vue`）：账号设置、版本管理、Fabric、下载队列、Mod 管理、启动表单、启动进度、开发者模式日志。

## 错误处理

必须明确区分并给出可操作提示：

- 未设置账号 / 微软设备码登录超时或取消 / 第三方认证失败；
- Java 不存在（提示按版本组件要求选择或自动下载）；
- 版本文件缺失或校验失败（sha1 不匹配自动重下）；
- natives 缺失或残留；
- 实例目录不可写；
- IP/端口非法；
- 游戏进程启动后立即退出（捕获 exit code + 日志尾段）。

## 安全要求

- access/refresh token 不得进入日志；启动命令日志脱敏；
- 账号凭据只存 WinApp 本地（微软/第三方 token），不写入 Panel 数据库；
- 用户选择目录必须做路径校验；
- 合并目录时不得删除用户源目录；清理只允许清理 WinApp 自己的 cache；
- 退出登录时清理本地 token。

## 分阶段实现回顾

- **阶段 1（已落地）**：离线账号 + 表单启动 + 版本安装 + 自动选 Java（下载运行时）；
- **阶段 2（已落地）**：微软设备码登录 + 第三方认证 + 版本设置持久化 + 下载队列与多源镜像；
- **阶段 3（已落地）**：Fabric 安装 + Mod 管理 + Modrinth 搜索/依赖安装 + 开发者模式；
- **后续方向**：按 PCL2 研究报告继续补齐下载/启动/UI 细节。

## 当前状态

「加入游戏」已作为完整可用的国际版启动能力交付，与面板「服务器监控」（国际版服务器在线人数监控）相互独立。
