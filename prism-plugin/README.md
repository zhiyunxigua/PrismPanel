# PrismMC

PrismMC 是 PrismPanel 的 Bukkit/Paper 子服插件基础框架，要求 Java 17 和 Spigot/Paper 1.18.2 或更高兼容版本。

## 构建

```powershell
mvn package
```

输出 JAR 位于 `target/PrismMC-0.1.0.jar`。将其放入 Minecraft 子服的 `plugins` 目录即可，不需要手工配置节点令牌。

## daemon 连接

daemon 每次启动实例时注入以下环境变量：

```text
PRISM_DAEMON_WS
PRISM_INSTANCE_ID
PRISM_SESSION_ID
PRISM_PLUGIN_TOKEN
```

插件只接受指向本机回环地址的 WebSocket URL。临时令牌绑定实例、启动代次和进程树，进程退出后立即失效。插件没有这些环境变量时仍可正常加载，但不会启用遥测。

插件建立连接后每 10 秒发送心跳，每 5 秒上报一次：

- TPS 和 MSPT（仅服务端实现支持时）
- JVM 堆内存和线程数
- 在线玩家、延迟和加入时间
- Bukkit/Paper 插件列表

当面板完成全服 OP 初始化后，Spigot/Paper 插件还会接收 daemon 合并后的本节点 OP 名单，拦截玩家和服务端控制台执行的 `op` / `deop`，并每 5 秒修复其他插件造成的 OP 状态漂移。代理插件不参与 OP 管理。

实例进程树的 CPU 和 RSS 由 daemon 独立采样，不由插件上报。
