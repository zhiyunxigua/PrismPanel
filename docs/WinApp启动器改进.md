# PrismPanel WinApp 启动器改进（参照 PCL2）

> 本文汇总 WinApp（`winapp/internal/game/`）参照 PCL2 源码（`EG/Plain Craft Launcher 2`）实施的改进。完整逐项对比与研究见 [PCL2-winapp改进建议.md](./PCL2-winapp改进建议.md)（353 行，速览表 29 条，含组件映射勘误）。

## 一、改进总览

| 批次 | 内容 | 状态 |
|---|---|---|
| 第一批（t5） | 下载/启动/Java/Fabric/Mod 低风险项 | ✅ 24/29 条落地（含 2 条中风险项与跟进项） |
| 跟进（t5 补充） | JVM dropUnresolved、Java 候选源、Fabric 单次请求、候选源域名分流 | ✅ 4 条 |
| 第二批（P2，t2） | natives 完整核对、Modrinth 依赖、中文用户名编码 | ✅ 3 条（本轮） |
| 留待后续 | Modrinth 依赖可选增强、natives 逐 dll 核对后续、崩溃分析移植 | 📌 远期 |

## 二、下载系统

- **sha1/size 校验**：client jar、资源索引、assets（hash+size）、libraries artifact、natives classifier、Modrinth mod 全部传校验值；`.part` 复用校验尺寸+sha1；损坏/半截文件自动重下（`downloadURLChecked` / `OnceChecked` / `OnceProgressChecked`）
- **429 退避 + 节流**：`StatusTooManyRequests` → 标记慢源 + 镜像节流 + 指数退避；BMCLAPI 100ms 节流
- **大文件长超时**：文件下载统一走 `mcHTTPClientLong`（30min 整体超时，保留拨号/响应头超时）；仅 JSON 元数据请求保留 60s（快速换源）
- **候选源按域名分流**（`mc_mirror.go`）：`libraries.minecraft.net`→maven+libraries 双候选、Fabric/Forge loader maven→maven 单候选、`piston-*`/launchermeta→root；loader 且非 meta 时官方源不兜底（PCL2 语义）
- **健康记忆**：主机失败跳过/慢速降级（10 分钟过期）；测速偏好官方源结果 10 分钟 TTL 重测；**用户取消任务不拉黑源**（`ctx.Err()` 检查）
- **下载队列**：并发 3、去重、取消/移除/清空、`prism:mc-download` 事件推送

## 三、启动参数

- **参数去重**：忠实移植 PCL2 `DeduplicateJavaArguments`——JVM 键值对完全重复删除、game 键值对后者覆盖、负值不误判
- **QuickPlay 自动进服**：1.20.2+ / 快照 23w31a+（精确按 `year==23 且 week>=31` 判定）→ `--quickPlayMultiplayer host:port`；老版本回退 `--server/--port`
- **JVM 占位符**：`${classpath}`/`${natives_directory}`/`${library_directory}`/`${primary_jar}`/`${auth_session}`/`${classpath_separator}`/`${libraries_directory}` 全覆盖 + **替换后 dropUnresolved 兜底**（防御未来新占位符，不把 `${...}` 字面量传给 Java）
- **打码日志**：启动命令脱敏（token/密码）后以 `launch-cmd` stage 记录并写入游戏日志文件
- **启动前补全检查**：缺失库/资源/原生库按版本 JSON 自动重下重解压（`mcMissingLaunchFiles`/`mcCompleteLaunchFiles`/`mcLaunchFilePlan`）
- **natives 启动前完整核对**（P2 #28）：按"文件名+大小"核对每个 dll/dylib/so，清版本升级残留（不误删运行中文件——Windows 文件锁保护），不匹配重解压，仍失败给出明确启动前错误
- **中文用户名编码**（P2 #12）：对齐 PCL2 L1366-1389——按 Java 大版本注入 stdout/stderr `file.encoding` 兼容参数（Java<19 `-Dsun.stdout/stderr.encoding=UTF-8`；18-20 加 `-Dfile.encoding=COMPAT`；≥19 stdout/stderr.encoding；≥21 不注入）；`--username` 经 Windows UTF-16 原生传递不经代码页转换
- **路径预检测**：`!/;` 非法字符拒绝；profile 解析区分"未安装/损坏"

## 四、Java 选择与下载

- **需求对齐**：1.16→8、1.17→16、1.18-1.20.4→17、1.20.5+→21；版本 JSON `javaVersion.component` 权威优先，缺失时按 `mcComponentForMajor` 回退
- **组件映射**（经 Mojang 官方数据三方实证 + 勘误）：`jre-legacy=8`、`alpha=16`、`beta/gamma=17`、`delta=21`、`epsilon=25`；`22+→epsilon` 前瞻分支（未来 majorVersion≥24 不缺 component 时不落错）
- **自动下载**：Java 运行时走候选源（官方 + BMCLAPI 镜像双源）+ 429/健康记忆/节流 + 30min 长超时；SHA-1 校验（跳过 PCL2 同款巨型文件哈希名单）；**下载后 `detectJavaVersion >= required` 校验**（不匹配报错而非静默使用）
- **扫描**：常见 JDK 目录 + 官启 runtime 目录（两级布局）

## 五、Fabric 安装

- **判装**：`versions/<id>/<id>.json` 存在才算已安装（防止空目录误判，可重装）
- **库下载**：并发下载 + 无 URL 时按 maven 坐标拼镜像地址兜底
- **profile JSON 单次请求**：`getRawBytes` 一次获取，解析 + 落盘同一字节（消除双下载）
- loader 版本经 `meta.fabricmc.net` 获取，镜像走 `fabric-meta` 映射

## 六、Mod 管理

- 列表/启停（`.disabled` 重命名）/删除/打开目录
- **Modrinth 搜索/安装**：覆盖安装 Bug 修复（Windows 下先删旧文件再改名）+ 下载 size 校验
- **依赖自动安装**（P2 #14）：解析项目版本 JSON `dependencies`，递归安装 required 依赖（依赖先于本体、visited 去重防环、深度上限 5、optional 跳过、尊重固定 `version_id`）；失败汇总明确错误（列出缺失依赖）

## 七、构建与验证

```bat
cd winapp
go build ./...        :: 或 go vet ./... / go test ./...
```

- 环境：Go 1.27（`C:\Program Files\Go\bin\go.exe`），`GOPROXY=https://goproxy.cn,direct`；沙箱内 GOCACHE/GOMODCACHE 指向工作区 `.gocache/.gomodcache`
- 验证：全量 `go build/vet/test ./...` 通过（含新增测试：natives 8、编码 6、依赖 7、QuickPlay 边界、dropUnresolved、组件映射等 20+ 用例）
- 已知环境坑（见 [DEVELOPMENT-NOTES.md](./DEVELOPMENT-NOTES.md)）：PowerShell 勿用 Get-Content/Set-Content 改 Go 源文件（GBK 乱码损坏）；`go test` 偶发被安全软件拦截（用 `go test -c -o` 后运行测试二进制）
