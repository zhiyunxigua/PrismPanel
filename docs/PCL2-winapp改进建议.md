# PCL2 启动器源码研究报告：WinApp 国际版改进建议

> 作者：researcher-pcl2（PrismPanel 团队）
> 日期：本报告基于 `EG/Plain Craft Launcher 2/`（PCL2 官方 VB.NET/WPF 源码，只读参考）与 `winapp/internal/game/` 现有 Go 实现逐文件对比后产出。
> 用途：为 engineer-winapp 提供可直接落地的改进依据。
>
> **风险标注说明**：
> - ✅【立即实施】改动小、不影响现有功能、收益明确，建议本轮直接做；
> - ⚠️【中风险/可选】改动较大或需前端配合，收益高但需评估；
> - 📌【仅参考】PCL2 特有复杂机制（如多线程单文件下载、JLW、崩溃分析全量移植），作为后续方向参考，不建议现阶段做。

---

## 0. 结论速览（给 engineer-winapp 的 Top 建议）

| # | 改进项 | 风险 | 对应文件 |
|---|--------|------|----------|
| 1 | 下载文件增加 sha1/size 校验（防静默损坏） | ✅ 低（t5 已落地） | `mc_version.go` |
| 2 | 启动前关键文件快速完整性检查 + 自动补全 | ✅ 低（t5 已落地） | `mc_launch.go` / `mc_version.go` |
| 3 | HTTP 429 退避 + BMCLAPI 请求频率限制 | ✅ 低（t5 已落地） | `mc_version.go` / `mc_mirror.go` |
| 4 | 启动参数去重（键值对去重移植） | ✅ 低（t5 已落地） | `mc_launch.go` |
| 5 | 启动参数/日志脱敏输出（token 打码） | ✅ 低（t5 已落地） | `mc_launch.go` / `process.go` |
| 6 | 自动进服 QuickPlay（1.20.2+，需版本 releaseTime） | ✅ 低 | `mc_launch.go` / `mc_version.go` |
| 7 | Fabric 库并发下载 | ✅ 低（t5 已落地） | `mc_fabric.go` |
| 8 | Java 运行时文件并发下载 | ✅ 低（t5 已落地） | `mc_java.go` |
| 9 | Modrinth 安装时处理"目标文件已存在"（Windows rename 覆盖失败） | ✅ 低（Bug 修复） | `mc_mods.go` |
| 10 | 版本清单解析 type/releaseTime | ✅ 低 | `mc_version.go` |
| 11 | Java 扫描增加官启 runtime 目录；需求估算对齐 PCL2（1.16→8、1.17→16、1.18+→17、1.20.5+→21） | ✅ 低（t5 已落地） | `mc_launch.go` / `mc_java.go` |
| 12 | 中文用户名/路径编码参数（sun.stdout/stderr.encoding 等） | ⚠️ 中 | `mc_launch.go` |
| 13 | 预检测（路径非法字符、版本状态） | ✅ 低 | `mc_launch.go` |
| 14 | Modrinth 依赖安装（dependencies 字段） | ⚠️ 中 | `mc_mods.go` |
| 15 | 崩溃日志分析（PCL2 ModCrash 精简移植） | 📌 参考 | 新文件 `mc_crash.go` |
| 16 | 多线程单文件下载（Range 分片） | 📌 参考 | `mc_version.go` |
| 17 | JLW（JavaWrapper 解决 Java 8 中文乱码） | 📌 参考 | `mc_launch.go` |
| 18 | GC 策略自动选择（G1GC/ZGC 按 Java 版本） | 📌 参考 | `mc_launch.go` |
| 19 | 取消下载时不要误拉黑宿主（ctx.Canceled 检查） | ✅ 低（Bug 修复，t5 已落地） | `mc_version.go` / `mc_mirror.go` |
| 20 | 官方源测速结果加 TTL（PCL2 每次刷新版本列表重测） | ✅ 低（t5 已落地，10min TTL） | `mc_mirror.go` |
| 21 | Java 运行时文件下载走镜像候选源（官方+镜像双源） | ✅ 低（t5 已落地） | `mc_java.go` |
| 22 | 文件下载不设整体 60s 超时（PCL2 只限制响应头；t5 已用 30min 长超时 client 全覆盖文件下载） | ✅ 低（t5 已落地） | `mc_version.go` / `mc_java.go` |
| 23 | java 组件映射按实测确认正确（alpha=16、beta=17、gamma=17、delta=21），无需修正 | ✅ 已确认（t5 实测验证） | `mc_java.go` |
| 24 | MCFabricInstalled 需校验 versions/<id>/<id>.json 存在 | ✅ 低（Bug 修复，t5 已落地） | `mc_fabric.go` |
| 25 | Fabric 库 URL 为空时按 maven 坐标拼镜像地址（不静默跳过） | ✅ 低（t5 已落地） | `mc_fabric.go` |
| 26 | Fabric profile JSON 只下载一次（getRawBytes 解析+落盘） | ✅ 低（t5 已落地） | `mc_fabric.go` |
| 27 | EnsureMCJava 下载完成后校验 Java 大版本 | ✅ 低（t5 已落地） | `mc_java.go` |
| 28 | 启动前校验 natives 完整性（按名+大小，PCL2 McLaunchNatives） | ⚠️ 中（t5 已做缺失时重解压的轻量版，完整版下轮） | `mc_launch.go` / `mc_version.go` |
| 29 | JVM 参数替换后 dropUnresolved 兜底（防御，PCL2 也不做但建议加） | ✅ 低 | `mc_launch.go` |

---

## 1. 版本下载与安装

### 1.1 PCL2 做法要点

**版本列表（双源 + 测速切换）**（`Modules/Minecraft/ModDownload.vb`）：
- `DlClientListMojangLoader`（L232-248）：官方 `https://launchermeta.mojang.com/mc/game/version_manifest.json`；首次加载时计时，`DeltaTime < 4000ms` 则本次会话 `DlPreferMojang = True`（官方源优先），否则镜像优先。
- `DlClientListBmclapiLoader`（L273-293）：镜像 `https://bmclapi2.bangbang93.com/mc/game/version_manifest.json`。
- `DlVersionListOrder` / `DlSourceOrder`（L1229-1245）：`ToolDownloadVersion` / `ToolDownloadSource` 设置（0=自动测速 / 1=镜像优先 / 2=官方优先）决定候选顺序：`OfficialUrls.Union(MirrorUrls)` 或反向。

**按类型改写镜像 URL**（`ModDownload.vb` L1251-1304）：
- `DlSourceAssetsGet`：`resources.download.minecraft.net` → `bmclapi2.bangbang93.com/assets`（含 piston-data/piston-meta 两个旧域名）。
- `DlSourceLibraryGet`：官方 `libraries.minecraft.net` → 镜像 `.../maven` 与 `.../libraries` 双候选；**fabricmc/minecraftforge/neoforged 的库不添加原版源**（官方 maven 会 404）。
- `DlSourceLauncherOrMetaGet`：launcher.mojang.com / launchermeta.mojang.com / piston-* → 镜像根路径。

**版本安装 = 补全文件**（`ModDownload.vb` L54-120 `DlClientFix`）：
- 分阶段 Loader：分析缺失支持库 → 下载支持库；下载资源索引 → 分析资源列表 → 下载资源文件；client jar 单独 `DlClientJarGet`（L9-28，用 `FileChecker` 校验 `size + sha1`，已通过校验则跳过）。
- 资源索引缺失时回退内置 legacy 索引（`ModMinecraft.vb` L2226-2259，sha1 `c0fd82e8...`）。
- **个别资源下载失败不中断安装**（部分失败容忍，全部失败才报错）。

**多线程下载引擎**（`Modules/Base/ModNet.vb` L302-1210）：
- `NetTaskThreadLimit = ToolDownloadThread + 1`（全局线程上限，可配置）。
- 单文件多线程：按 Range 分片（`NetThread` 链表），每线程写独立临时文件（`Thread.Temp`，即 `.part` 体系），下载完成后合并；首线程先拿 `Content-Length`。
- 下载中途源失败（`SourceFail` L1070-1096）：连续失败超过阈值或首次连接失败即禁用该源，其余线程自动换源续传（"第一个线程出错时切换下载源"）。
- 429 处理：`ModNet.vb` L73 `If StatusCode = 429 Then Thread.Sleep(10000)`；且每次给 BMCLAPI 发请求后 `Thread.Sleep(100)` 降频。
- 下载管理线程每 ~0.1s 刷新进度与速度（L1674-1792）。
- 文件检查：`FileChecker`（size + md5/sha1），校验失败删除重下（L1214-1234）。

### 1.2 现有 winapp 实现差距

`winapp/internal/game/mc_version.go` / `mc_mirror.go` / `mc_queue.go` 已有：
- ✅ `mcCandidateURLs` 按 PCL2 `DlSourceOrder` 语义实现（官方/镜像顺序、loader 库不加官方源、assets 镜像、fabric-meta 镜像），并有官方源测速（`mcProbeOfficialSource`，<4s 官方优先，对应 PCL2 `DlPreferMojang`）。
- ✅ 下载宿主健康记忆（10 分钟内失败跳过、慢速排后）。
- ✅ assets 并发下载（`mcEffectiveConcurrency`，默认 16）、libraries 并发下载。
- ✅ `.part` 临时文件 + `reuseCompletePart`（长度一致直接改名复用）。
- ✅ 多版本下载队列（`MCDownloadManager`，最多 3 个版本并行）。

**差距**：
1. **无 sha1/size 校验**：`downloadURLTo` 只要文件存在就跳过（`fileExists(destination) → return nil`），不校验内容；下载完成只按 `Content-Length` 判断。官方 version JSON 的 `downloads.client.sha1/size`、`libraries[].downloads.artifact.sha1/size`、asset index 的 `objects[].hash/size` 都提供了校验值，当前完全没用上 → 半截/损坏文件会被静默保留，启动时表现为类路径错误等难排查问题。
2. **启动前无"补全文件"环节**：PCL2 每次启动都会跑 `DlClientFix` 检查缺失/损坏文件；winapp `LaunchMC` 只解析 profile 直接拼参数，库缺失直接 Java 报错。
3. **429 无退避**：仅 5xx 标记宿主失败；429 会立刻重试（且镜像/官方都可能被打）。
4. **版本清单丢弃元数据**：`mcManifest` 只解析 `id/url`，`MCVersionEntry` 定义了 `Type/ReleaseTime` 但 `FetchMCVersions` 不填（影响排序、QuickPlay 判定、Java 版本估算）。
5. 单文件下载是单线程整文件（PCL2 支持 Range 分片多线程）——现阶段可接受。

### 1.3 改进建议

- ✅【立即实施】**下载校验**：给 `downloadURLOnce/downloadURLOnceProgress` 增加可选 `wantSize int64 / wantSHA1 string` 参数（0/空表示不校验）；调用处（client jar、asset、library、version JSON）从 version JSON / asset index 传入校验值；校验失败删 `.part` 重试下一候选源。`reuseCompletePart` 也改成"尺寸+sha1 都匹配才复用"。
- ✅【立即实施】**启动前完整性检查**：`LaunchMC` 在解析 profile 后、启动 Java 前，检查 `ClientJar`、`NativesDir` 内 dll、`AssetIndexID.json`、所有 `LibraryPaths` 是否存在；缺失则提示并可调用现有下载函数补全（复用 `downloadURLTo`，逐文件并发）。
- ✅【立即实施】**429 退避**：`downloadURLOnce*` 中 `response.StatusCode == 429` 时 `time.Sleep(2~10s)` 再试下一候选（PCL2 睡 10s）；并在 `mcOrderCandidates` 里对 `bmclapi2.bangbang93.com` 做 100ms 间隔节流（PCL2 做法）。
- ✅【立即实施】**版本清单补元数据**：`mcManifest.Versions` 增加 `Type/ReleaseTime` 字段并透传（1 行改动，后续 QuickPlay/排序都要用）。
- ⚠️【中风险/可选】**断点续传**：失败时保留 `.part` 并记录已下载长度，重试时用 `Range: bytes=N-` 续传（PCL2 的 `NetThread` 思路的简化版）。对国内网络大文件（client jar ~50MB）收益明显。
- 📌【仅参考】**多线程单文件下载**：PCL2 的 Range 分片 + 合并 + 源切换是完整工程，改动大；建议先做断点续传即可。

---

## 2. 启动参数构建

### 2.1 PCL2 做法要点

**整体流程**（`Modules/Minecraft/ModLaunch.vb`）：
- `McLaunchArgumentMain`（L1182-1209）：JVM 参数 + mainClass + 游戏参数 → 全部做 `${...}` 替换 → **含 `& | < > ^ 空格` 的参数加双引号**（内部 `"` 转义为 `\"`）→ 拼接输出，且每个参数 `McLaunchLog` 输出。
- `McLaunchArgumentsJVM`（L1212-1398）：
  - 新版：取 `arguments.jvm`，字符串直接加、对象按 `rules`（`McJsonRuleCheck`）过滤后取 `value`（字符串或数组）；**沿继承链向上合并**（`CurrentInstance.InheritName` 循环）。
  - 旧版（无 arguments.jvm）：`-XX:HeapDumpPath=MojangTricksIntelDriversForPerformance_javaw.exe_minecraft.exe.heapdump`、`-Djava.library.path=${natives_directory}`、`-cp ${classpath}`。
  - 自定义 JVM 参数（版本级/全局）经 `ArgumentReplace` 预替换后混入。
  - authlib-injector：`-javaagent:...authlib-injector.jar=<server>` + `-Dauthlibinjector.side=client` + prefetch（base64 预取响应）；Nide 通行证同理。
  - **JLW**（L1282-1297）：非 GBK 编码环境用 `JavaWrapper.jar` 包装启动（JDK-8272352 中文乱码修复），Java 9+ 加 `--add-exports`。
  - **内存管理**（L1305-1361）：`-Xmx<ram>m`；GC 策略按 Java 大版本自动选 G1GC / ZGC / 分代 ZGC（Java 21+），用户可设 0-3 档；`-XX:+UseCompactObjectHeaders`（Java 24+）。
  - Log4j 防御：`-Dlog4j2.formatMsgNoLookups=true`。
  - **编码参数**（L1366-1389）：Java 21+ UTF-8；18-20 加 `-Dfile.encoding=COMPAT`；17- 按系统 ANSI；`-Dsun.stdout.encoding`/`-Dsun.stderr.encoding`（Java<19 用 `sun.` 前缀）显式指定，防止中文控制台乱码。
  - `DeduplicateJavaArguments`（L1613+）：键值对（如两个 `--width`）只保留最后/第一个，单参数完全重复删除；`-xPos 23 -xPos -50` 这种负值不被误判为参数。
- `McLaunchArgumentsGame`（L1400-1499）：
  - 旧版 `minecraftArguments` 字符串 + **总是追加 `--height ${resolution_height} --width ${resolution_width}`**（后续去重时覆盖 MC 自带的）。
  - 新版 `arguments.game` 同 rules 过滤；沿继承链合并。
  - **自动进服**（L1476-1495）：`ReleaseTime > 2023-04-04`（1.20.2+）用 `--quickPlayMultiplayer <server>`；老版本 `--server <host> --port <port>`（无端口默认 25565）；带 OptiFine 时提示不兼容。
  - OptiFineForgeTweaker 移到参数末尾、`OptiFineTweaker` 自动修正。
- `McLaunchArgumentsReplace`（L1501-1575）：
  - 占位符全集：`${classpath_separator}`、`${natives_directory}`、`${library_directory}`、`${libraries_directory}`、`${pure_directory}`、`${launcher_name}`、`${launcher_version}`、`${version_name}`、`${game_directory}`、`${assets_root}`、`${user_properties}`、`${auth_player_name/uuid/access_token/session}`、`${user_type}`（固定 "msa"）、`${primary_jar}`、`${game_assets}`（assets\virtual\legacy）、`${assets_index_name}`、`${resolution_width/height}`、`${classpath}`。
  - **`${version_type}` 为空时删除 `--versionType` 参数对**（否则游戏显示空字符串）。
  - **短路径**：所有路径 `PathUtils.ToShortPath`（8.3 格式）——路径含空格时 classpath 不加引号也能工作，这是 Windows 下 PCL2 稳定性的关键。
  - classpath 中 OptiFine 固定放倒数第二位。
- `SplitJavaArguments`（L1580-1609）：引号/转义引号感知的参数分割。

### 2.2 现有 winapp 实现差距

`winapp/internal/game/mc_launch.go` 已有：
- ✅ `ResolveMCLaunchProfile` 合并 base + Fabric（libraries、arguments、mainClass、minecraftArguments 回退链）。
- ✅ `filterMCArguments` 按 rules 过滤（OS/arch），支持字符串/数组 value。
- ✅ 占位符替换表较全（含 `${classpath}`、`${natives_directory}` 等），旧版参数回退、`-Djava.library.path`/`-cp` 兜底补齐。
- ✅ 内存参数统一 `-Xmx` 覆盖、authlib-injector `-javaagent` 注入、第三方认证 prefetch（`EnsureMCAuthlibInjector`）。

**差距**：
1. **无参数去重**：只对已知参数 `upsertFlagValue`；版本 JSON 里自带重复键值对（如两个 `--width`）会原样保留，可能双双失效导致崩溃（PCL2 注释明确说过这点）。
2. **无参数日志**：不输出最终启动参数（`LaunchResult` 无 args 字段），排查问题只能靠猜；PCL2 把每个参数都打进日志并做用户名/token 打码。
3. **`--versionType` 未处理空值**：winapp 固定替换为 `"release"`，无自定义入口——功能上 OK，但无 PCL2 的"删参数"能力（可忽略）。
4. **无编码参数**：中文用户名/中文路径在 Java 8/17 下控制台 GBK 乱码问题未处理（服务器面板用户是中文名的场景会出现日志乱码/异常）。
5. **无自动进服 QuickPlay 分支**：总是 `--server/--port`；1.20.2+ 官方推荐 QuickPlay。
6. **窗口宽高**：winapp 只在请求显式给 Width/Height 时 upsert；无默认 854x480、无 DPI 修复（PCL2 对 1.12.2- + JRE 8u200-321 有 DPI 倍率修复）。
7. **无 JLW / GC 策略**：均未实现（见"仅参考"）。
8. **APPDATA 环境变量**：winapp 已做（`APPDATA=<mcDir>`），与 PCL2 思路一致 ✅。

### 2.3 改进建议

- ✅【立即实施】**参数去重**：移植 `DeduplicateJavaArguments` 简化版——遍历参数列表，`-` 开头且下一项不是 `-`/负数字时视为键值对，按键去重（保留最后一个）；纯单参数完全重复删除。在 `BuildMCLaunchArgs` 返回前调用。
- ✅【立即实施】**启动参数日志**：`LaunchMC` 组装完 args 后，把 `javaPath + args`（对 `account.AccessToken` 打码为 `*`，PCL2 `FilterAccessToken` 思路）写入日志文件或 report 回调，前端可展示"查看启动命令"。
- ✅【立即实施】**QuickPlay**：`mcManifest` 补 releaseTime 后，`BuildMCLaunchArgs` 里 `--server` 分支判断 `releaseTime > 2023-04-04` → 用 `--quickPlayMultiplayer <ip:port>`（QuickPlay 接受 `host:port` 单参数）。
- ✅【立即实施】**预检测**：`LaunchMC` 开头增加路径检查（游戏根路径含 `!`/`;` 直接报错，对应 PCL2 `McLaunchPrecheck` L228-229）、profile 解析失败给出"版本未安装/需补全"的明确提示。
- ⚠️【中风险/可选】**编码参数**：根据所选 Java 大版本追加 `-Dsun.stdout.encoding`/`-Dsun.stderr.encoding`（Java<19 用 `sun.` 前缀，≥19 用 `stdout.encoding`）+ Java 18-20 加 `-Dfile.encoding=COMPAT`；对中文用户名场景收益明确，改动小（10 行内）。
- ⚠️【中风险/可选】**默认窗口尺寸**：Width/Height 为 0 时补 `854x480` 默认（PCL2 `LaunchArgumentWindowType` 默认值），并处理 1.12.2- 的 DPI 修复（可选，服务器面板场景价值低）。
- 📌【仅参考】**JLW（JavaWrapper.jar）**：解决 Java 8 下中文用户名/路径乱码，需要内置 `JavaWrapper.jar` + `-jar` 包装 + `--add-exports`，改动大且引入新组件；现代 Java 17/21 已默认 UTF-8，仅老版本（1.12.2-）用户需要。
- 📌【仅参考】**GC 策略自动选择**：winapp 已允许用户自定义 JVM 参数（`JVMArgs`），默认让用户自己写 `-XX:+UseG1GC` 即可；做自动选择收益一般。

---

## 3. Fabric 安装

### 3.1 PCL2 做法要点

- **不执行官方 fabric-installer.jar**，而是直接取 meta 生成的 profile JSON（`Pages/PageDownload/ModDownloadLib.vb` L1650-1656）：
  - 候选源：`https://bmclapi2.bangbang93.com/fabric-meta/v2/versions/loader/<mc>/<loader>/profile/json`（优先）+ `https://meta.fabricmc.net/v2/versions/loader/<mc>/<loader>/profile/json`（兜底）；
  - 写入 `versions/<fabric-loader-<loader>-<mc>>/<id>.json`，`FileChecker.IsJson` 校验。
- 安装完成后**通过 `DlClientFix` 补全 libraries**（版本 JSON 里的 libraries 走 `DlSourceLibraryGet` 镜像规则，见 1.1）。
- Fabric 版本列表双源（`ModDownload.vb` L1107-1156）：`meta.fabricmc.net/v2/versions` + `bmclapi2.bangbang93.com/fabric-meta/v2/versions`，按 `ToolDownloadVersion` 设置决定顺序，超时 5s/30s/60s 自动切换（`DlSourceLoader` L1319-1387：前一个源失败或超时后启动下一个）。
- Fabric 版本号提取：从 libraries 字符串正则 `(?<=net.fabricmc:fabric-loader:)[0-9\.]+(\+build.[0-9]+)?`（`ModMinecraft.vb` L388-389）。
- Fabric API / OptiFabric 直接走 Modrinth/CurseForge 资源下载（`DlFabricApiLoader` L1161-1162）。

### 3.2 现有 winapp 实现差距

`winapp/internal/game/mc_fabric.go` 已有：
- ✅ `FetchMCFabricLoaders`（meta.fabricmc.net，`getJSON` 会走 `mcCandidateURLs` → fabric-meta 镜像兜底）。
- ✅ `InstallMCFabric`：下载 profile/json（走镜像候选）→ 逐个下载 libraries（按 maven 坐标推导本地路径，含 4 段 classifier）。
- ✅ 防重复安装、`DeleteMCVersion` 处理 Fabric 子版本删除、`MCFabricVersionFor` 自动选已装 Fabric 子版本启动。

**差距**：
1. **libraries 串行下载**：Fabric 约 20-40 个库，逐个下载明显慢；assets/libraries 都已并发，这里漏了。
2. **无"分析缺失文件"步骤**：如果用户手动删了某个 fabric 库，重装时 `downloadURLTo` 会跳过已存在的（OK），但启动前没有补全检查（见 1.3-2）。
3. profile/json 的 `url` 字段在 Fabric 库上不总是可信（部分库无 url → winapp 跳过），PCL2 用 `DlSourceLibraryGet` 兜底拼 maven 地址——winapp 无此兜底。Fabric meta 现在一般都有 url，风险低。
4. 无 Fabric API 快捷安装入口（PCL2 有 `DlFabricApiLoader`；winapp 可通过 Modrinth 搜索装，等价）。

### 3.3 改进建议

- ✅【立即实施】**libraries 并发下载**：`InstallMCFabric` 用 `sem := make(chan struct{}, mcEffectiveConcurrency())` + WaitGroup 并发下载（复制 `downloadMCLibraries` 的模式），失败收集首个错误。
- ✅【立即实施】**maven 兜底**：库 `URL == ""` 时按 `fabricLibraryPath(name)` 生成 BMCLAPI maven 地址（`bmclapi2.bangbang93.com/maven/<path>`），再不行用官方 `https://maven.fabricmc.net/<path>`（对应 PCL2 `DlSourceLibraryGet` 语义）。
- ⚠️【中风险/可选】**安装后完整性报告**：返回已下载/缺失库列表，前端可展示。

---

## 4. Java 运行时

### 4.1 PCL2 做法要点

- **需求分析**（`Modules/Minecraft/ModJava.vb` L128-269 `GetJavaRequirement`）：以"版本范围交集"模型求 Java 需求：
  - 按 MC 版本：1.20.5+ → ≥21；1.18 pre2+ → ≥17；1.17+ → ≥16；1.12+ → ≥8；1.5.2- → ≤8；
  - `javaVersion.majorVersion >= 22` 时**以 Mojang 权威值为准**（`RecommendedComponent` 取 `javaVersion.component`）；
  - OptiFine/Forge/NeoForge/Fabric/LiteLoader 各自叠加上下限约束（如 1.16.5 Forge 34.0.0-36.2.25 上限 8u320、Fabric 1.18+ ≥17 等）。
- **选择策略**（L276-468 `SelectOrDownloadJava`）：版本文件夹内 Java（设置 2）→ 强制指定（设置 3）→ 自动从 JavaList 选（`CheckAsync` 跑 `java -version` 校验 + 版本范围匹配）→ 重扫 → **都没有则自动下载 Mojang 运行时**。
- **自动下载**（L541-569 `CreateJavaDownloadLoader`）：
  - all.json：`https://piston-meta.mojang.com/v1/products/java-runtime/2ec0cc96c44e5a76b9c8b7c39df7210883d12871/all.json` + BMCLAPI 镜像（`bmclapi2.bangbang93.com/v1/products/java-runtime/...`）；
  - 选组件：优先 `javaVersion.component`（如 `java-runtime-gamma`），否则按版本范围匹配（jre-legacy / java-runtime-alpha / beta / gamma / delta）；
  - 逐文件下载到 `.minecraft/runtime/<component>/`，`size + sha1` 校验；
  - **跳过 3 个巨型重复文件**（sha1：`12976a6c2b227cbac58969c1455444596c894656`、`c80e4bab46e34d02826eab226a4441d0970f2aba`、`84d2102ad171863db04e7ee22a259d1f6c5de4a5`，见 L553-554 注释 #3827）。
- 版本探测：`JavaUtils.SearchFoldersAsync` 扫描常见目录（含官方启动器 runtime 目录）。

### 4.2 现有 winapp 实现差距

`winapp/internal/game/mc_java.go` + `mc_launch.go` 已有：
- ✅ 同款 all.json（固定 revision）+ BMCLAPI 镜像（`getJSON` 候选）；组件名优先用版本 JSON `javaVersion.component`（权威），缺失时才用大版本 fallback 映射。
- ✅ 跳过 3 个巨型重复文件（`javaPackedSkipHashes`，与 PCL2 完全一致）。
- ✅ 逐文件 `size + sha1` 校验 + `.part` 临时文件。
- ✅ `FindMCJavaForVersion`：版本 JSON `javaVersion.majorVersion` 权威 → 估算 → 设置 `DefaultJava` → `PRISMPANEL_JAVA_PATH` → `JAVA_HOME` → 常见 JDK 目录扫描（Java/Adoptium/Microsoft/Zulu/Corretto/BellSoft）→ PATH。
- ✅ `detectJavaVersion` 解析 `java -version`（支持 `1.8.0_402` 老格式）。

**差距**：
1. **Java 运行时文件串行下载**：`EnsureMCJava` 逐个文件下载（Java 21 组件约 200+ 文件、60-100MB），国内网络很慢；PCL2 走多线程下载引擎。→ ✅ 已修复（t5：semaphore 并发）。
2. **常见目录扫描不全**：不含官方启动器自带 runtime（`%LOCALAPPDATA%\Packages\Microsoft.4297127D64EC6_8wekyb3d8bbwe\LocalCache\Local\runtime\`）和 `.minecraft/runtime`；装了官启的用户会被重复下载一份 Java。→ ✅ 已修复（t5：`FindMCJavaForVersion` 已扫描官启 runtime + 自身缓存 + 版本目录 runtime）。
3. **Java 需求估算偏差（已修复）**：t5 已按 PCL2 `GetJavaRequirement`（ModJava.vb L152-171）修正 `mcJavaRequirement`：1.12-1.16 → "8"；1.17 → "16"；1.18-1.20.4 → "17"；1.20.5+（含 2.x）→ "21"；快照 23w31a+ → "21"。**注意**：组件 fallback 映射经 t5 实测确认**正确**（见下方"组件映射勘误"），无需修改。
4. **无 Java 列表概念**：PCL2 维护可校验的 JavaList 并在 UI 展示；winapp 只有"默认路径 + 自动发现"。
5. **无版本强制校验**：`mcJavaSatisfies` 探测失败时"按存在处理"（`return true`），可能选中坏 Java。
6. **EnsureMCJava 下载完成后不校验大版本**：只检查 `bin/java.exe` 存在（`findJavaInStore`），不跑 `detectJavaVersion` 核对实际版本；PCL2 下载后接 `JavaListRefreshWorker` 刷新并 `CheckAsync` 校验（ModJava.vb L566）。→ ✅ 已修复（t5：下载后 `detectJavaVersion` 比对 `>= required`）。
7. **Java 文件下载不走镜像候选源**：`downloadJavaFile`（mc_java.go L197-238）直接用原始 URL + `mcHTTPClient.Do`，没有 `mcCandidateURLs`、没有健康记忆、没有 429 处理；PCL2 用 `DlSourceOrder({Url}, {Url.Replace("piston-data.mojang.com", "bmclapi2.bangbang93.com")})`（ModJava.vb L558）官方+镜像双源兜底。→ ✅ 已修复（t5：`downloadJavaFile` 走 `mcCandidateURLs` + 429 退避/健康记忆/节流/覆盖重下；实测 BMCLAPI 镜像根路径可命中 piston-data 对象与 java-runtime all.json）。

### 4.3 改进建议

- ✅【已落地（t5）】**Java 文件并发下载**、**扫描官启 runtime 目录**、**需求估算修正**、**Java 文件走候选源**、**下载后校验大版本** —— 全部完成，见 4.2 各条。
- 📌【组件映射勘误】t5 实测（逐一拉取 Mojang 版本 JSON 的 `javaVersion`）确认 Mojang 组件语义为：**jre-legacy=8、java-runtime-alpha=16（1.17）、java-runtime-beta=17（1.18）、java-runtime-gamma=17（1.19-1.20.4，更新的 17 系 build）、java-runtime-delta=21（1.20.5+）**；当前实现 `16→alpha、17→gamma、21→delta` **与实测一致，是正确的**。本报告早前"gamma=21、delta=24"的表述有误，予以更正（勘误依据：Manjaro 论坛实证"java-runtime-gamma (Java 17.0.3)"）。
- ⚠️【中风险/可选】**Java 列表 API**：暴露 `JavaScan()`（返回本机发现的 Java 及大版本）+ 让前端可选 Java（现有 `MCVersionSettings.JavaPath` 已支持指定，只差扫描列表），对用户手动指定有用。
- 📌【仅参考】**JavaList 校验/修复 UI**、32 位 Java 检测（`Invalid maximum heap size` 崩溃分析覆盖）。

---

## 5. Mod 管理

### 5.1 PCL2 做法要点

- PCL2 **没有独立 mods 目录管理页**（不做启停），但资源下载能力强（`Pages/PageDownload/` + `Modules/Resource/ResourceVersion.vb`）：
  - `DlModRequest`（`ModDownload.vb` L1178-1211）：**Mod 镜像源 mcimirror**（`api.modrinth.com → mod.mcimirror.top/modrinth`、`cdn.modrinth.com → mod.mcimirror.top`、`api.curseforge.com → mod.mcimirror.top/curseforge` 等），按 `ToolDownloadMod` 设置（0=镜像优先 / 1=官方优先 / 2=官方）排列候选，官方 Modrinth 超时给到 20s（"Modrinth 返回不过来"）。
  - 安装目标：`<游戏目录>\mods\`（`PageDownloadCompDetail.xaml.vb` L355）。
  - `ResourceVersion.FromProjectId` 支持按 loader/game_version 过滤 + **加载依赖（LoadDependencies）**。
- `ModWatcher.vb` 是**游戏进程监控**（非目录监控），用于启动进度/崩溃检测（见第 6 节）。

### 5.2 现有 winapp 实现差距

`winapp/internal/game/mc_mods.go` 已有：
- ✅ mods 目录扫描（`.disabled` 后缀启停）、toggle/delete/open dir。
- ✅ Modrinth 搜索（facets：`versions:<gameVersion>` + `categories:<loader>`，limit 24）。
- ✅ Modrinth 安装（`/project/<id>/version` 过滤 game_versions/loaders → 取首个文件下载）。
- ✅ mcimirror 镜像候选（`mcModrinthCandidates` 与 PCL2 `DlSourceModGet` 一致）。
- ✅ 文件名安全校验（`mcSafeFilename`，拒绝 `/ \ :` 和隐藏文件）。

**差距**：
1. **目标文件已存在时覆盖失败（Bug）**：`MCModrinthInstall` 下载到 `mods/<filename>`；若同名文件已存在，`os.Rename(tmp, destination)` 在 **Windows 上会失败**（Go 的 rename 不覆盖已存在目标），用户重装/更新 mod 会报错。应先 `os.Remove(destination)` 再 rename，或先写临时名再替换。
2. **无依赖安装**：Modrinth version 的 `dependencies` 字段（前置 mod，如 fabric-api）未处理；PCL2 有 LoadDependencies 选项。
3. **无 mod 详情/更新检查**：搜索返回 `latest_version` 但无"已安装版本 vs 最新"比对（前端可做，需 API 支持 project 版本列表——已有 `/project/<id>/version`）。
4. 搜索不区分 loader 的"任意"（loader 为空时 facets 只有 versions，OK）。

### 5.3 改进建议

- ✅【立即实施】**修复覆盖**：`MCModrinthInstall` 下载前 `os.Remove(target)`（或 `os.Rename` 失败时先删再重试一次）；同时删除 `target+".disabled"` 已有逻辑保留。
- ✅【立即实施】**下载完成校验**：Modrinth file 的 `size` 字段可用于下载后比对（`downloadURLOnce` 校验 `ContentLength` 已有，再加 size 一致性检查）。
- ⚠️【中风险/可选】**依赖安装**：解析 `MCModrinthVersion.Dependencies`（type=required + project_id），逐个递归安装到同目录；需前端确认或默认自动装 required 依赖（PCL2 默认关，给用户选项）。
- ⚠️【中风险/可选】**更新检查**：新增 `MCCheckModUpdates(versionID)` 读取每个 jar 的 `fabric.mod.json`/`mods.toml` 的 modid + 版本，比对 Modrinth 最新版本（需 API 支持，工作量大，建议后置）。

---

## 6. 其他值得借鉴（错误提示 / 日志 / 启动前检查 / 崩溃分析）

### 6.1 PCL2 做法要点

**分阶段启动流水线**（`ModLaunch.vb` L113-137）：预检测 → 获取 Java → 登录 → 补全文件 → 获取启动参数 → 解压 Natives → 预启动处理 → 执行自定义命令 → 启动进程 → 等待游戏窗口出现 → 结束处理；每阶段有 `ProgressWeight`，UI 实时显示阶段名与进度。

**预检测**（L225-280 `McLaunchPrecheck`）：
- 路径含 `!`/`;` 直接拒绝启动；
- 版本 JSON 加载失败/状态错误给出明确中文提示；
- 登录信息校验（`McLoginAble`）。

**日志**：
- `McLaunchLog`（L71-75）：所有启动步骤、每个启动参数都记日志，**用户名与 accessToken 打码**（`FilterUserName`/`FilterAccessToken` 替换为 `*`），防止 token 泄漏进日志/崩溃报告。
- `Logger` 分级（Info/Warn/Error/Trace），异常 `GetDisplay` 递归展开 inner exception。

**启动监控**（`ModWatcher.vb`）：
- 5 步进度：日志输出 → `Setting user:` → `lwjgl version` → `OpenAL initialized` → 材质加载完成 → 判定 Running（L195-211）。
- 崩溃识别：`Crash report saved to` / `Could not save crash report to` / Forge `Unable to launch` / `An exception was thrown...`（L215-236）。
- 窗口检测：Win32 `EnumWindows` 匹配类名 `GLFW30/SDL_app/LWJGL/SunAwtFrame` + 进程 PID 校验（L312-342）。
- 崩溃后延迟 2 秒触发分析（L359-373）。

**崩溃分析**（`ModCrash.vb`）：收集 `latest.log` + `crash-reports/*` + `hs_err_pid*.log` → 正则匹配 100+ 种原因（内存不足、Java 版本过高/不兼容、OpenJ9、32 位 Java、显卡驱动、OptiFine×Forge 不兼容、Mod 重复/缺少前置/互不兼容、Mixin 失败、配置损坏、特定方块/实体崩溃、Fabric "A potential solution" 解析等，L484-670）→ 输出中文解决方案列表（L861-1110）。

**预启动处理**（`McLaunchPrerun` L1738+）：高性能显卡注册表设置（`SetGPUPreference`）、微软登录时更新 `launcher_profiles.json`、`options.txt` 初始化（含 yosbr mod 兼容）、`launcher_profiles.json` 损坏时删除重建。

### 6.2 现有 winapp 实现差距

- ✅ 已有阶段化 report 回调（`report(stage, message, percent)`）、进程日志落盘（`DefaultGameLogPath` + `cmd.Stdout/Stderr → logFile`）、`APPDATA` 指向游戏目录。
- ❌ **无预检测**（路径非法字符、版本状态检查）。
- ❌ **无启动参数日志**（无法在日志中复现启动命令）。
- ❌ **无日志打码**：账号 token 可能进入 report 消息或日志（`auth_access_token` 只在参数里，进程日志是游戏 stdout，风险较低；但 report 消息无 token，安全面尚可——仍需注意未来扩展）。
- ❌ **无游戏进度监控**（5 步日志进度、窗口出现判定、崩溃识别）——winapp 启动即返回，前端只能看"已启动"。
- ❌ **无崩溃分析**：崩溃后没有任何原因诊断，用户只能自己翻日志。
- ❌ 无自定义启动前后命令（PCL2 `McLaunchCustom`）。

### 6.3 改进建议

- ✅【立即实施】**预检测**：`LaunchMC` 开头检查 `mcDir`/`versionDir` 路径不含 `!`/`;`；profile 解析失败时区分"版本未安装"与"版本 JSON 损坏"（现在 `ResolveMCLaunchProfile` 已给"未安装"提示，补充损坏提示）。
- ✅【立即实施】**启动参数打码日志**：组装 args 后写一行 `[launch] java -Xmx... <打码后的 args>` 到日志文件（token → `***`），同时通过 report 回调 `stage="launch-cmd"` 展示给前端（排查神器）。
- ✅【立即实施】**进程退出码记录**：`ProcessManager.Start` 的 `cmd.Wait()` goroutine 里记录退出码到日志（PCL2 Watcher L160-178 的退出判定简化版），前端可判断"异常退出（code!=0）"。
- ⚠️【中风险/可选】**游戏进度事件**：读游戏日志（日志文件已落盘，tail 即可）匹配 `Setting user:` / `lwjgl version` / `OpenAL initialized` 关键字 → 通过现有事件通道推给前端显示"加载到 XX%"；崩溃关键字（`Crash report saved to`）→ 标记 crashed。
- ⚠️【中风险/可选】**崩溃日志收集**：游戏异常退出时，收集 `crash-reports/latest.txt`、`hs_err_pid*.log` 的最后 N 行存入 winapp 日志目录，供前端查看。
- 📌【仅参考】**崩溃原因分析**：移植 PCL2 `ModCrash` 的匹配表（可先做 Top 20 高频原因：OutOfMemory、Java 版本、Mod 重复、显卡驱动 EXCEPTION_ACCESS_VIOLATION、`Unsupported class file major version` 等），输出中文建议。工作量中等，但这是"启动器体验"差距最大的地方。
- 📌【仅参考】**自定义启动前/后命令**、launcher_profiles.json 更新、显卡偏好设置。

---

## 附：源码引用索引（方便 engineer-winapp 直接查证）

| 主题 | PCL2 源码位置 | winapp 对照文件 |
|------|--------------|----------------|
| 版本列表/镜像源 | `Modules/Minecraft/ModDownload.vb` L181-300、L1215-1391 | `mc_version.go`、`mc_mirror.go` |
| 补全文件 | `ModDownload.vb` L9-120；`ModMinecraft.vb` L2141-2269 | `mc_version.go` |
| 多线程下载引擎 | `Modules/Base/ModNet.vb` L302-1210 | `mc_version.go`（并发部分） |
| 下载队列/线程管理 | `ModNet.vb` L1674-1792；`Modules/Base/ModLoader.vb` | `mc_queue.go` |
| 启动参数 JVM | `ModLaunch.vb` L1212-1398 | `mc_launch.go` |
| 启动参数 Game | `ModLaunch.vb` L1400-1499 | `mc_launch.go` |
| 占位符替换 | `ModLaunch.vb` L1501-1575 | `mc_launch.go` |
| 参数分割/去重 | `ModLaunch.vb` L1580-1654 | `mc_launch.go`（缺） |
| 启动流水线/预检测 | `ModLaunch.vb` L40-280 | `mc_launch.go` `LaunchMC` |
| 启动监控/窗口/崩溃识别 | `ModWatcher.vb` 全文 | `process.go`（缺） |
| 崩溃分析 | `ModCrash.vb` L21-670、L861-1110 | 无 |
| Java 需求/选择/下载 | `ModJava.vb` L128-569 | `mc_java.go`、`mc_launch.go` |
| Fabric 安装 | `ModDownloadLib.vb` L1600-1662；`ModDownload.vb` L1087-1168 | `mc_fabric.go` |
| Mod 镜像/安装 | `ModDownload.vb` L1172-1211；`ResourceVersion.vb` | `mc_mods.go` |
| 预启动处理 | `ModLaunch.vb` L1738-1837 | 无 |

---

## 7. engineer-winapp 疑点逐条核实结论（对照 PCL2 源码）

> 本节针对 engineer-winapp 预读 `winapp/internal/game` 后提出的 4 组疑点，逐条给出"PCL2 怎么做的（含源码引用）→ 结论 → 建议"。结论分为：✅ 疑点成立（有真差距/真 Bug）、◽ 部分成立、❌ 疑点不成立（winapp 现状已与 PCL2 等价或更优）。

### 7.1 占位符处理

**疑点 1a：${quickPlayPath/Singleplayer/Multiplayer/Realms}（1.20.2+ 版本 JSON）未替换**

- **PCL2 怎么做**：`McLaunchArgumentsReplace`（ModLaunch.vb L1501-1575）**没有** quickPlay 相关占位符；`McLaunchArgumentsGame`（L1400-1499）把 1.20.2+ 版本 JSON 里 `--quickPlayPath ${quickPlayPath}` 等原样加入参数列表。PCL2 的"自动进服"逻辑（L1476-1495）是：`ReleaseTime > 2023-04-04` 时**追加** `--quickPlayMultiplayer <server>`，其余 quickPlay 参数不替换（保留字面量传给游戏——游戏端对无效 quickplay 路径会静默忽略，不崩溃）。
- **结论**：◽ 部分成立。PCL2 不替换也不删除 quickPlay 占位符（靠游戏容错）；winapp 的 `dropUnresolvedPlaceholderArgs`（mc_launch.go L275 对 game args）反而**更干净**：会把含 `${` 的参数及其前导 `--quickPlayXXX` 一并删掉。
- **建议**：✅ 保持现状（drop 比 PCL2 留字面量好）；但注意 **JVM 参数（L229）没有 dropUnresolved 兜底**——JVM 段若出现未知 `${...}` 会原样传给 Java 导致启动失败，建议给 `jvmArgs` 也套一层 `dropUnresolvedPlaceholderArgs`（防御性，低风险；PCL2 对 JVM 段同样不删，但 winapp 加一层成本为零）。

**疑点 1b：${user_properties} 硬编码 "{}"、${auth_xuid} 硬编码 "0"（微软账号下 PCL2 填真实数据）**

- **PCL2 怎么做**：ModLaunch.vb L1513 `Yield ("${user_properties}", "{}")`——**PCL2 也是硬编码 "{}"**（注释 `#1221` 只对 user_type 写死 "msa"）；全源码 grep `xuid` **零匹配**，PCL2 对 `${auth_xuid}` 完全**不做替换**（1.20.2+ JSON 里的 `--xuid ${auth_xuid}` 原样保留，靠游戏端容错）。
- **结论**：❌ 疑点不成立。PCL2 并未在微软账号下填真实 xuid/user_properties；winapp 填 `"0"`/`"{}"` 与 HMCL 等主流启动器一致，且比 PCL2 留字面量更规范。真实 xuid 仅对微软正版多人联机有影响（XSTS 响应的 `xid`），而本项目场景（连接面板服务器）以离线/第三方认证为主，`"0"` 足够。
- **建议**：✅ 保持现状；若未来要支持微软正版多人，可在 `MCMicrosoftAuthenticate`（mc_auth.go L244-272）解析 XSTS 响应的 `xid` 存入 `MCAccount` 并填入 `${auth_xuid}`（参考官方启动器行为，非 PCL2 参照）。

### 7.2 下载源健康记忆

**疑点 2a：用户取消任务（ctx.Canceled）时误把源拉黑 10 分钟（mcHostMarkFailed）**

- **PCL2 怎么做**：ModNet.vb 下载线程取消走 `State = NetState.Canceled` 分支（L1037、L1151），**不调用** `SourceFail`（L1070-1096）；`SourceFail` 只由真实网络错误/校验失败触发。即取消 ≠ 源失败，不会污染源健康。
- **winapp 现状**：`downloadURLOnce`（mc_version.go L583-587）、`downloadURLOnceProgress`（L671-673）、`getJSON`（L511-513）在 `http.Do` 返回 err 时**无条件** `mcHostMarkFailed(request.URL.Host)`；`ctx.Canceled` 时 `Do` 返回的正是 ctx 错误 → 取消一次下载，官方/镜像被拉黑 10 分钟。
- **结论**：✅ 疑点成立，是真实 Bug（用户取消会恶化后续所有下载）。
- **建议**：✅ 在 `mcHostMarkFailed` 前判断 `if ctx.Err() != nil { 跳过标记 }`（三处 `downloadURLOnce` / `downloadURLOnceProgress` / `getJSON` 统一处理）。低风险。→ ✅ **t5 已落地**（mc_version.go L531/L547、mc_java.go L264/L277/L293 等均加 `ctx.Err() == nil` 守卫）。

**疑点 2b：mcPreferOfficial 测速结果进程生命周期内永久缓存（PCL2 每次会话/定期重测？）**

- **PCL2 怎么做**：`DlPreferMojang`（ModDownload.vb L1217）在 `DlClientListMojangMain`（L233-248）**每次加载版本列表时重新测速赋值**；`DlClientListLoader` 是 LoaderTask，普通调用复用缓存结果，但**强制刷新版本列表（IsForceRestart）时重跑**。即 PCL2 是"每次刷新/重载版本列表重测"，不是永久缓存。
- **winapp 现状**：`mcSourcePrefersOfficial`（mc_mirror.go L257-264）只在 `mcPreferOfficial < 0` 时测一次，之后进程生命周期内永不复测；网络环境变化（如用户切换代理）后判断失效。
- **结论**：✅ 疑点成立（缓存粒度比 PCL2 粗）。
- **建议**：✅ 给测速结果加 TTL（如 10~30 分钟过期重测），或在 `FetchMCVersions`/`InstallMCVersion` 每次入口重测。低风险。→ ✅ **t5 已落地**（mc_mirror.go L265-275：`mcPreferOfficialAt` + `time.Since > mcHostBlockDuration`(10min) 重测）。

**疑点 2c：downloadJavaFile 不走候选源/健康记忆**

- **PCL2 怎么做**：Java 文件下载（ModJava.vb L558）`New NetFile(DlSourceOrder({Url}, {Url.Replace("piston-data.mojang.com", "bmclapi2.bangbang93.com")}), ...)`——**官方 + BMCLAPI 镜像双源**，走统一下载引擎（含源切换/健康管理）。
- **winapp 现状**：`downloadJavaFile`（mc_java.go L197-238）直接 `http.NewRequestWithContext` 原始 URL，**无 `mcCandidateURLs`、无 `mcHostMarkFailed`、无 429 处理**；官方源不可达时 Java 下载直接失败。
- **结论**：✅ 疑点成立（真实差距，国内网络下 Java 运行时下载无镜像兜底）。
- **建议**：✅ `downloadJavaFile` 改为遍历 `mcCandidateURLs(raw.URL)`（保留 sha1/size 校验与 `.part`），并在网络错误（非 ctx 取消）时 `mcHostMarkFailed`。低风险。→ ✅ **t5 已落地**（mc_java.go L235-251 候选源循环 + `downloadJavaFileOnce` L253-313 含 429 退避/健康记忆/节流/覆盖重下；实测 BMCLAPI 镜像根路径命中 piston-data 与 java-runtime all.json）。

**疑点 2d：大文件走全局 60s Timeout 可能整体超时**

- **PCL2 怎么做**：下载引擎用 `HttpCompletionOption.ResponseHeadersRead` + `GetResultWithTimeout(CancelToken, Timeout)`（ModNet.vb L917-919），**Timeout 只作用于响应头**；body 用流式循环读取（L994-1045），由取消令牌控制，无整体超时。
- **winapp 现状**：`mcHTTPClient.Timeout = 60s`（mc_version.go L24-35）是**整体超时**（含 body 读取）；client jar（~50MB）、Java 包（单文件数十 MB）在慢速网络下会整体 60s 中断。
- **结论**：✅ 疑点成立（慢速网络大文件下载必现）。
- **建议**：⚠️ 文件下载改用**不带整体 Timeout 的 client**（保留拨号 15s / 响应头 30s 的超时，去掉 `Timeout` 字段），靠 ctx 取消 + 进度上报兜底；JSON 请求仍用现有 60s client。中风险（需小心回归，建议先给 `downloadURLOnce*` 换 client）。→ ✅ **t5 已完整落地**：新增 `mcHTTPClientLong`（30min 整体超时，复用 mcHTTPClient 的 Transport 保留拨号/响应头超时，mc_version.go L40-46）；`downloadURLOnceChecked`（L692）、`downloadURLOnceProgressChecked`（L817）、Java `downloadJavaFileOnce`（mc_java.go L262）统一走长超时 client——**assets（downloadAssetWithRetry→downloadURLChecked）、libraries artifact、natives classifier、client jar、Modrinth mod 文件下载全部覆盖**。仍用 60s client 的仅为 JSON 元数据请求（getRawBytes L528、Modrinth API 搜索/版本查询 mc_mods.go L197/L256），小响应快速换源，属有意保留。

**疑点 2e：assets/libraries 已存在文件不校验 size/hash**

- **PCL2 怎么做**：`FileChecker`（ModDownload.vb L23、L552 等）对每个文件做 `size + sha1` 校验，通过才跳过，损坏即重下。
- **winapp 现状**：`downloadURLTo`（mc_version.go L543-545）`fileExists(destination) → return nil`，只看存在不看内容。
- **结论**：✅ 疑点成立（与第 1 条建议相同，报告 1.3-1 已覆盖）。
- **建议**：✅ 见 1.3-1：下载前/后按 `size + sha1`（version JSON / asset index 提供）校验，不符则重下。→ ✅ **t5 已落地**（`downloadURLChecked`/`downloadURLOnceChecked`/`downloadURLWithProgress` 增加 `wantSize/wantSHA1` 校验，`fileMatchesExpectation` + `.part` 复用也校验 sha1）。

### 7.3 Fabric 安装

**疑点 3a：MCFabricInstalled 只看 versions/ 目录存在与否（空目录误判已安装）**

- **PCL2 怎么做**：版本"已安装"以 **versions/<id>/<id>.json 存在**为准（McInstance 的 `GetJsonPath()`/`Load()`，ModLaunch.vb L43-54、ModMinecraft.vb 版本扫描），不只看目录名。
- **winapp 现状**：`MCFabricInstalled`（mc_fabric.go L108-127）遍历 `versions/` 下任何 `fabric-loader-` 前缀目录就返回 true，**不检查 json 是否存在**；残留空目录会误判 → 用户无法重装。
- **结论**：✅ 疑点成立（真实 Bug；`MCFabricVersionFor` L179-187 反而检查了 json，两者不一致）。
- **建议**：✅ `MCFabricInstalled` 增加 `versions/<name>/<name>.json` 存在性检查（与 `MCFabricVersionFor` 对齐）。低风险。→ ✅ **t5 已落地**（mc_fabric.go L162+，含测试 mc_launch_test.go L268/L280 覆盖）。

**疑点 3b：URL 为空的库静默跳过、无 rules 处理**

- **PCL2 怎么做**：`McLibNetFilesFromTokens`（ModMinecraft.vb L2141-2194）对库逐个构造 URL：有 URL 用 URL，无 URL 时**按 maven 坐标拼镜像地址**（L2186-2188 `DlSourceLibraryGet`：`libraries.minecraft.net` 或 BMCLAPI maven），并处理 rules（`McLibEntry`）；Fabric 库走 `DlSourceLibraryGet`（ModDownload.vb L1264-1289，fabricmc 库不添加原版源）。
- **winapp 现状**：`InstallMCFabric`（mc_fabric.go L69-70）`if library.URL == "" || library.Name == "" { continue }` **静默跳过**；`fabricLibrary` 结构（L30-33）无 rules 字段，遇到带 rules 的库（个别第三方 meta 会带）直接忽略。
- **结论**：✅ 疑点成立（URL 为空时库缺失 → 启动时 classpath 缺文件）。
- **建议**：✅ URL 为空时按 `fabricLibraryPath(name)` 推导 BMCLAPI maven 地址（`bmclapi2.bangbang93.com/maven/<path>`）兜底（PCL2 同款逻辑）；`fabricLibrary` 增加可选 `Rules` 字段按 `mcRulesAllow` 过滤（防御性，Fabric 官方 meta 基本不带 rules，低优先级）。低风险。→ ◽ **部分落地（t5）**：`InstallMCFabric` 对无 URL 库已按 maven 坐标推导镜像地址（mc_fabric.go L92 `fabricLibraryPath`）；rules 过滤未做（Fabric 官方 meta 基本无 rules，可忽略）。

**疑点 3c：profile JSON 下载两次**

- **PCL2 怎么做**：`McDownloadFabricLoader`（ModDownloadLib.vb L1650-1656）用 `NetFile(候选源, VersionFolder & Id & ".json", FileChecker IsJson)` **一次下载写文件**，随后从文件解析。
- **winapp 现状**：`InstallMCFabric` 先 `getJSON(ctx, endpoint, &profile)` 拉一次（L52-55），又 `downloadURLTo(ctx, endpoint, ...)` 再下载一次写文件（L60）——同一 URL 请求两次。
- **结论**：✅ 疑点成立（浪费一次网络请求；且两次请求间源可能切换，文件与解析结果不一致）。
- **建议**：✅ `getJSON` 拿到后直接把响应字节写入版本 JSON 路径（或先 `downloadURLTo` 再读文件解析），只请求一次。低风险。→ ✅ **t5 已落地**（mc_fabric.go 用 `getRawBytes` 取原始字节，解析 + 原样落盘；`getJSON` 也重构为基于 `getRawBytes`）。

**疑点 3d：EnsureMCJava 下载后不校验大版本、组件映射 beta/gamma/delta 对应是否错误**

- **PCL2 怎么做**：`CreateJavaDownloadLoader`（ModJava.vb L541-569）下载完接 `JavaListRefreshWorker`（L566 "刷新 Java 列表"）重新扫描并 `CheckAsync` 校验版本；组件选择**不靠硬编码映射**，而是解析 all.json 每个组件的 `version.name`（L388-396 解析出真实 Java 版本）后按**需求范围匹配**（L409-411：优先 `javaVersion.component`，否则选范围内组件）。
- **winapp 现状（t5 已修正/实测）**：
  - `EnsureMCJava` 下载完只查 `bin/java.exe` 存在（mc_java.go L217-220）——**已修复**：收尾用 `detectJavaVersion` 核对 `>= required`（L221-226）。
  - 组件映射经 **t5 实证**（逐一拉取 Mojang 版本 JSON 的 `javaVersion` 字段）确认：**1.16.5→jre-legacy(8)；1.17.1→java-runtime-alpha(16)；1.18.2→java-runtime-beta(17)；1.19.2/1.20.4→java-runtime-gamma(17)；1.20.6/1.21.1/1.21.4→java-runtime-delta(21)**——即 **alpha=16、beta=17、gamma=17（1.19+ 更新的 17 系 build）、delta=21**。当前实现 `16→alpha、17→gamma、21→delta` **正确**。
  - 本报告早前称"gamma=21、delta=24/25，映射写反"**有误，予以更正**（勘误依据：[Manjaro 论坛实证"java-runtime-gamma (Java 17.0.3)"](https://forum.manjaro.org/t/need-help-with-dri-prime-1-for-atlauncher/150175/36#6)，以及 t5 对 Mojang 版本 JSON 的实测）。
- **结论**：◽ 部分成立。下载后不校验大版本 → 已修复；组件映射 → 实测确认正确（"映射写反"不成立）。
- **建议**：✅ 无需再改映射；保留"已装版本走权威 component"逻辑即可（t5 已确认）。
  - **epsilon 前瞻（已落地）**：engineer 证据程序（`.tmpcheck/main.go`，拉取 all.json → 各组件 manifest → `release` 文件的 `JAVA_VERSION`）实测：**jre-legacy=1.8.0_51、alpha=16.0.1、beta=17.0.15、gamma=17.0.15、delta=21.0.7、epsilon=25.0.1**。已采纳前瞻建议：`mcJavaComponent` 重构出 `mcComponentForMajor`（mc_java.go L85-98），新增 default 分支 **22+ → java-runtime-epsilon**（避免未来版本 JSON `majorVersion≥24` 且缺 component 字段时 fallback 错落到 delta），并新增 `TestMCComponentForMajor` 直接覆盖 24/25/26→epsilon（mc_java_test.go L32-47）。go build/vet/test 全绿。

### 7.4 其他

**疑点 4a：natives 启动前不校验完整性（PCL2 按名+大小核对）**

- **PCL2 怎么做**：`McLaunchNatives`（ModLaunch.vb L1657-1718）**每次启动都执行**：打开每个 natives zip（打不开→删文件并抛"可能已损坏，请重新尝试启动游戏"）、对 zip 内每个 `.dll` 检查目标已存在且 `Length == Entry.Length` 则跳过，否则删除重解压（L1682-1699）、最后**删除 natives 目录中多余残留文件**（L1705-1716）；natives 目录还特意用短路径/非 ASCII 回退（L1722-1731，避免中文路径 DLL 加载问题）。
- **winapp 现状**：`downloadMCNatives`（mc_version.go L407-432）只在**安装时**解压一次，启动前不核对；若 natives 目录被误删/损坏/残留旧版 dll，启动静默失败（LWJGL 加载错误难排查）。
- **结论**：✅ 疑点成立（PCL2 每次启动按名+大小核对并清理残留）。
- **建议**：⚠️ `LaunchMC` 在解析 profile 后对 `NativesDir` 做快速核对：对每个 natives 库 zip 的 dll 列表比对"存在 + 大小一致"，缺失/不符则重新解压（可顺带清理多余 dll）；并在启动失败日志中提示 natives 状态。中风险（涉及 zip 读取逻辑，可复用 `extractZipFiltered`）。

**疑点 4b：MCModsList 不读 jar 内 fabric.mod.json/mods.toml**

- **PCL2 怎么做**：PCL2 **没有 mods 列表管理页**（不做 mod 启停/元数据展示）；`TryAnalyzeModName`（ModCrash.vb L843-854）是从崩溃日志文本里提取 mod 名，不是读 jar；`ModModpack.vb` L279 读 zip 内文件列表仅用于整合包识别（检查是否含 META-INF/mcmod.info/fabric.mod.json）。
- **结论**：❌ 疑点不成立（PCL2 本身不读 mod jar 元数据，无参照可对齐）。这是 winapp 的自有增强空间（HMCL 有：显示 mod 名/版本/作者）。
- **建议**：⚠️ 可选增强：`MCModsList` 解析每个 mod jar 内的 `fabric.mod.json`（id/version/name）或 `mods.toml`（modId/version/displayName），返回友好名称；失败时回退文件名。需加 zip 读取 + 容错，工作量中等；若前端只按文件名管理，可不做。

---

## 附：实施状态与优先级（给 engineer-winapp 的执行顺序）

**✅ 已落地（t5，engineer-winapp 完成，go build/vet/test 全绿）**：1（sha1/size 校验）、2（启动前补全检查）、3（429 退避 + 节流）、4（参数去重）、5（参数打码）、7（Fabric 并发）、8（Java 并发）、11（需求估算 + 官启 runtime 扫描）、19（取消不拉黑源）、20（测速 10min TTL）、21（Java 文件走镜像候选）、22（文件下载走 30min 长超时 client 全覆盖，仅 JSON 元数据保留 60s）、24（Fabric 判装加 json 校验）、25（Fabric 库 URL 兜底）、26（Fabric JSON 单次请求 getRawBytes）、27（Java 下载后校验版本）、29（JVM 参数 dropUnresolved 兜底）；另优化 `mcCandidateURLs` 按域名分流（libraries→maven+libraries、loader maven→maven 单候选、piston-*→root 单候选，去掉必 404 的三连探）。**组件映射经实测确认正确（alpha=16、beta=17、gamma=17、delta=21），无需改动**；epsilon 前瞻已落地（`mcComponentForMajor` 22+→epsilon，测试覆盖 24/25/26）。

**待办（⚠️ 中风险）**：6（QuickPlay，需 releaseTime）、12（编码参数）、14（Modrinth 依赖）、28（natives 启动前"按名+大小"核对 + 清残留的完整版——当前为缺失时重解压的轻量版）、游戏进度事件 + 崩溃日志收集、4b（mod jar 元数据，可选）。

**远期（📌 参考）**：15（崩溃分析）、16（Range 分片下载）、17（JLW）、18（GC 策略）。
