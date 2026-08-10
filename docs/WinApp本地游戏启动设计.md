# WinApp 本地游戏启动设计

## 背景

当前网络游戏功能主要负责采集和展示网易网络游戏在线人数。新的“加入游戏”能力不再绑定网络游戏列表，而是面向用户本机启动一个 Java Minecraft 客户端，并连接到用户输入的服务器地址。

该能力必须放在 WinApp 侧实现，原因是：

- 启动 Java 进程必须发生在用户本机；
- Java 运行时、Minecraft 客户端文件、mods、config、resourcepacks、shaderpacks 都属于本地文件；
- 网易邮箱、密码、token、设备信息属于用户本地敏感数据，不应进入 Panel 数据库；
- Panel Web 端运行在浏览器环境，不能可靠管理本地进程和本地文件。

参考项目 `reference/Fantnel` 的实现方式是：集中缓存 Java 和基础客户端，按游戏和角色生成运行时实例目录，启动前准备认证 socket/RPC、拼接 JVM 和 Minecraft 参数，然后用 `Process.Start(javaPath, args)` 启动游戏。

本设计采用相同方向，但第一版只做必要能力，避免直接变成完整启动器。

## 目标

第一阶段目标：

- WinApp 提供“加入游戏”入口；
- 未登录网易账号时弹出本地登录窗口；
- 网易账号凭据只保存在用户本地；
- 用户可以输入 IP、端口、角色用户名；
- 用户可以选择游戏版本；
- 用户可以选择实例目录；
- WinApp 自动创建常见目录；
- 启动本地 Java 客户端并连接指定 IP/端口；
- 提供清晰的启动前检查和错误提示。

非目标：

- 第一版不做租赁服；
- 第一版不绑定网络游戏列表；
- 第一版不做多网易账号池；
- 第一版不把网易账号密码保存到 MySQL；
- 第一版不做复杂 Mod 市场或资源包管理；
- 第一版不保证所有线上服务器都兼容网易认证客户端。

## 用户流程

### 首次启动

1. 用户点击“加入游戏”。
2. WinApp 检查本地是否已有网易账号凭据。
3. 如果没有，弹出网易账号登录窗口：
   - 网易邮箱；
   - 网易密码。
4. 登录成功后，本地保存：
   - 邮箱；
   - 加密密码或可刷新登录凭据；
   - 设备信息；
   - X19 user id / token。
5. 显示加入游戏表单。

### 加入游戏表单

字段：

- IP：默认 `127.0.0.1`；
- 端口：默认 `25565`；
- 角色用户名：必填；
- 游戏版本：下拉选择；
- 实例目录：用户选择本地目录；
- JVM 参数：高级设置，第一版可隐藏；
- 最大内存：默认值，例如 `2048M` 或沿用 WinApp 设置。

用户点击“启动”后：

1. 校验 IP、端口、角色用户名、游戏版本、实例目录；
2. 检查网易登录状态，必要时自动刷新登录；
3. 检查 Java 运行时；
4. 检查 Minecraft 版本文件；
5. 创建 runtime 目录；
6. 合并基础客户端和实例目录；
7. 启动本地认证服务；
8. 拼接启动参数；
9. 启动 Java 进程；
10. 返回进程状态和日志入口。

## 本地数据存储

### 敏感数据

保存位置：WinApp 本地凭据存储。

Windows 下优先使用 Windows Credential Manager / DPAPI。

保存内容：

- 网易邮箱；
- 网易密码或登录刷新所需凭据；
- MPay 设备信息；
- X19 user id；
- X19 token；
- 最近验证时间。

要求：

- 不写入 Panel MySQL；
- 不输出到日志；
- 不返回给前端页面明文展示；
- 删除本地账号时必须清理关联 token 和设备信息。

### 非敏感偏好

可以保存到 WinApp 本地 settings：

- 默认 IP；
- 默认端口；
- 默认角色用户名；
- 默认游戏版本；
- 默认实例目录；
- 最大内存；
- 额外 JVM 参数。

如果后续需要多设备同步，可以只把非敏感偏好同步到 Panel 用户设置 JSON。

## 目录设计

WinApp 管理自己的游戏缓存目录：

```text
%LocalAppData%/PrismPanel/game-cache/
  java/
    jre8/
    jdk17/
    jdk21/
  base/
    .minecraft/
      assets/
      libraries/
      versions/
  runtime/
    {serverID}/
      assets/
      libraries/
      versions/
      mods/
      config/
      resourcepacks/
      shaderpacks/
  downloads/
  logs/
```

用户选择的是实例目录：

```text
F:\Documents\Minecraft\PrismInstance\
  mods/
  config/
  resourcepacks/
  shaderpacks/
  options.txt
```

如果用户目录缺少以下目录，WinApp 自动创建：

```text
mods
config
resourcepacks
shaderpacks
saves
logs
```

### 为什么不直接在用户目录启动

直接在用户目录启动会污染用户原始文件，也容易在调试时覆盖用户配置。

推荐做法：

1. 用户目录作为实例源目录；
2. WinApp 每次创建或复用 runtime；
3. 将基础客户端复制到 runtime；
4. 将用户实例目录中的 mods/config/resourcepacks/shaderpacks 合并到 runtime；
5. 游戏进程使用 `runtime/{serverID}` ??? 作为 gameDir。

这样便于清理、排错和回滚。

## Java 管理

参考 Fantnel 的选择规则：

- `>= 1.20.6` 使用 JDK 21；
- `>= 1.16` 使用 JDK 17；
- `< 1.16` 使用 JRE 8。

第一版可以采用两种策略：

### MVP 策略

只检查本地是否存在对应 Java：

- WinApp 内置配置 Java 路径；
- 或在 game-cache/java 下寻找；
- 找不到就提示用户安装或选择 Java 路径。

优点：

- 实现快；
- 风险低；
- 不涉及大文件下载。

### 完整策略

后续接入自动下载：

- 获取 Java 包下载地址和 md5；
- 下载到 `game-cache/downloads`；
- 解压到 `game-cache/java`；
- 写入 md5 文件；
- md5 匹配时跳过下载。

参考 Fantnel：

- `LaunchMessage.ExEnvironmentByJava`
- `PathUtil.Jre8Path`
- `PathUtil.Jre17Path`
- `PathUtil.Jre21Path`

## 游戏版本管理

第一版支持固定版本下拉：

- `1.8.9`
- `1.12.2`
- `1.16`
- `1.18`
- `1.19.2`
- `1.20`
- `1.20.6`
- `1.21`
- `1.21.8`
- `1.21.10`

版本映射采用内部枚举，参考 Fantnel：

```text
1.8.9   -> 1008009
1.12.2  -> 1012002
1.16    -> 1016000
1.18    -> 1018000
1.19.2  -> 1019002
1.20    -> 1020000
1.20.6  -> 1020006
1.21    -> 1021000
1.21.8  -> 1021008
1.21.10 -> 1021010
```

### MVP 策略

只检查版本文件是否存在：

```text
game-cache/base/.minecraft/versions/{version}/{version}.json
game-cache/base/.minecraft/versions/{version}/{version}.jar
```

缺失时提示用户：

- 当前版本未准备；
- 请先导入客户端；
- 或等待后续自动下载版本功能。

### 完整策略

后续接入网易 `/game-patch-info`：

- 下载基础包；
- 下载版本包；
- 下载 core libs；
- 校验 md5；
- 解压到 `game-cache/base/.minecraft`；
- 将依赖库安装到 `libraries` 或 `versions/{version}`。

参考 Fantnel：

- `InstallerService.PrepareMinecraftClient`
- `InstallerService.InstallCoreLibs`
- `NPFLauncher.GetMinecraftClientLibsAsync`

## Mod 和资源管理

本设计区分四类内容：

### 基础客户端内容

位置：

```text
game-cache/base/.minecraft
```

包含：

- assets；
- libraries；
- versions。

### 用户实例内容

位置由用户选择，例如：

```text
F:\Documents\Minecraft\PrismInstance
```

包含：

- mods；
- config；
- resourcepacks；
- shaderpacks；
- options.txt。

### Runtime 内容

位置：

```text
game-cache/runtime/{serverID}
```

启动前生成或刷新。

合并规则：

1. 复制基础客户端到 runtime；
2. 合并用户实例目录；
3. 用户实例内容优先覆盖 runtime 中同名文件；
4. 不反向写回用户实例目录；
5. 启动结束不自动删除 runtime，便于排查；
6. 提供手动清理缓存能力。

### 网易核心 Mod / 认证组件

完整启动网易客户端通常还需要核心组件和本地认证服务配合。

第一版建议先不自动下载网易游戏组件，只支持用户自备可启动的版本和 mod 目录。

后续再补：

- 查询核心 mod 列表；
- 下载核心 mod；
- 下载游戏组件包；
- 解压到缓存；
- 启动前复制到 runtime/mods。

参考 Fantnel：

- `InstallerService.InstallGameMods`
- `InstallerService.InstallCoreMods`
- `InstallerService.PrepareGameRuntime`

## 启动参数

启动命令不是简单 `java -jar`，需要拼接：

- JVM 参数；
- classpath；
- mainClass；
- natives 路径；
- gameDir；
- assetsDir；
- auth player name；
- uuid；
- access token；
- server；
- port；
- userProperties；
- userPropertiesEx。

参考 Fantnel 关键参数：

```text
-DlauncherControlPort={socketPort}
-DlauncherGameId={gameId}
-DuserId={userId}
-DToken={encryptedToken}
-DServer=RELEASE
-Djava.library.path={nativesPath}
--server {ip}
--port {port}
--username {roleName}
--uuid {uuid}
--gameDir {runtime/{serverID}}
--assetsDir {base/.minecraft/assets}
```

其中：

- `socketPort` 是本地认证服务端口；
- `userId/token` 来自网易登录；
- `uuid` 可按角色名和 userId 生成稳定 UUID；
- `userProperties` 和 `userPropertiesEx` 需要按版本差异生成。

## 本地认证服务

Fantnel 会在启动前启动：

- AuthLib socket；
- Game RPC service。

这说明只拼启动参数可能不够。若目标客户端依赖网易认证组件，WinApp 也需要实现本地认证服务。

第一版可以分两步：

1. 先实现启动命令和本地账号登录；
2. 如果实际客户端进服或启动认证失败，再补 AuthLib socket/RPC。

但从风险判断，正式可用版本大概率需要本地认证服务。

## API 和模块划分

### WinApp 后端模块

建议新增：

```text
winapp/internal/game/
  account.go
  launcher.go
  java.go
  version.go
  runtime.go
  mods.go
  process.go
```

职责：

- `account.go`：网易账号本地登录和凭据存储；
- `java.go`：Java 选择和检查；
- `version.go`：版本枚举和版本文件检查；
- `runtime.go`：runtime 目录准备；
- `mods.go`：实例目录合并；
- `launcher.go`：启动流程编排；
- `process.go`：进程状态和关闭。

### WinApp 暴露给前端的方法

```go
NetEaseAccountStatus() 
LoginNetEase(email, password)
LogoutNetEase()
GameLaunchDefaults()
PrepareGameInstance(input)
LaunchGame(input)
RunningGames()
CloseGame(processID)
```

### 前端页面

入口：

- 左侧导航或顶部按钮：“加入游戏”。

弹窗：

- 未登录网易账号：网易登录弹窗；
- 已登录：加入游戏表单。

启动状态：

- 检查中；
- 准备 runtime；
- 启动中；
- 已启动；
- 启动失败。

## 错误处理

必须明确区分：

- 未登录网易账号；
- 网易登录失败；
- 需要验证码或二次验证；
- Java 不存在；
- 游戏版本不存在；
- 实例目录不可写；
- IP/端口非法；
- 角色用户名为空；
- Java 进程启动失败；
- 游戏进程启动后退出；
- 本地认证服务端口被占用。

错误信息要面向用户可操作，例如：

```text
未找到 1.20.6 所需 JDK 21，请在设置中选择 JDK 21 路径或先安装运行时。
```

## 安全要求

- 网易密码不得进入 Panel 数据库；
- 网易密码不得进入日志；
- token 不得进入日志；
- 启动命令日志必须脱敏 `-DToken`；
- 用户选择目录必须做路径校验；
- 合并目录时不得删除用户源目录；
- 清理 runtime 只允许清理 WinApp 自己的 cache/runtime；
- 删除网易账号时清理本地凭据。

## 分阶段实现

### 阶段 1：最小可启动版本

- WinApp 本地保存网易账号；
- 加入游戏表单；
- 固定版本下拉；
- 用户选择实例目录；
- 创建缺失目录；
- 检查 Java 和版本文件；
- 准备 runtime；
- 拼接启动参数；
- 启动 Java 进程；
- 显示进程状态。

### 阶段 2：运行时完善

- 自动选择 Java；
- 支持配置 Java 路径；
- 支持导入基础客户端；
- 支持查看启动日志；
- 支持关闭游戏进程；
- 支持清理 runtime。

### 阶段 3：自动下载

- 自动下载 Java；
- 自动下载基础客户端；
- 自动下载版本包；
- 自动下载 core libs；
- md5 校验；
- 下载进度展示。

### 阶段 4：网易组件兼容

- 下载核心 Mod；
- 下载游戏组件包；
- 实现或移植 AuthLib socket；
- 实现或移植 Game RPC service；
- 完整兼容网易 Java 客户端启动。

## 当前建议

先做阶段 1。

原因：

- 能快速验证启动参数、目录结构和账号登录是否成立；
- 不引入大量下载、解压、md5、RPC 复杂度；
- 用户已经可以指定本地已有实例目录；
- 后续再按实际失败点补网易认证服务和自动下载。

