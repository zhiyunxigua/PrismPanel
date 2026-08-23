# prism-fabric（Fabric mod 运行态上报客户端）

向 PrismPanel daemon 上报 Fabric 服务端**实际加载**的 mod 列表（id / 版本 / 来源 jar），
使 mods.list 具备运行态确认能力（loaded / not_loaded / version_mismatch /
disabled_pending_restart 等），协议与合并逻辑见
[daemon 侧设计](../../docs/mod运行态上报设计.md)。

## 设计要点

- 只依赖 fabric-loader 公开 API（`net.fabricmc.loader.api.*` 与 `net.fabricmc.api.ModInitializer`），
  **不引用任何 Minecraft 类**，因此构建不需要 fabric-loom / yarn mappings，
  产物可跨 MC 版本使用（要求 Fabric Loader >= 0.14，服务端 Java >= 17）。
- 复用 `:core` 的 `PrismCore` / `DaemonBridge` / `PrismEnvironment`：
  连接、认证、10s 心跳、5s snapshot、断线退避重连、实例重启代次失效全部继承现有机制。
- 通过 daemon 注入的环境变量（`PRISM_DAEMON_WS` / `PRISM_INSTANCE_ID` /
  `PRISM_SESSION_ID` / `PRISM_PLUGIN_TOKEN`）认证；缺失时静默禁用（客户端误装不崩游戏）。
- mod 列表只上报 `ModOrigin.Kind.DIRECT` 来源（mods/ 顶层 jar），排除
  fabric-api 子模块（NESTED）与 `minecraft` / `fabricloader` / `java` 及自身 `prism-fabric`。

## 构建

需要 JDK 17+（无 Java 环境时无法本地构建，源码结构与配置已完整交付）。

```bat
cd prism-plugin
gradlew.bat :fabric:shadowJar
```

产物：`prism-plugin/fabric/build/libs/prism-fabric-0.2.0.jar`

## 部署

把产物放入 Fabric 服务端 `mods/` 目录后重启服务端即可；也可作为 fabric 制品上传
PrismPanel 插件仓库，经部署管线分发到目标服务端。

## 验证

daemon 侧 `mods.list` 出现运行态状态（如 `loaded`），且服务端控制台出现
`[Prism] Prism Fabric integration enabled` 日志。
