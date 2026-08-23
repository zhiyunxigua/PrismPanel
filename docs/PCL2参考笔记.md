# PCL2 参考笔记（Plain Craft Launcher 2 源码要点）

> 本启动器多处实现对齐 PCL2。源码位置：`EG/Plain Craft Launcher 2/`。最后更新：2026-08-23。

## 启动流程（Modules/Minecraft/ModLaunch.vb）
- `McLaunchStart` → `McLaunchPrecheck`（路径/版本/登录校验）→ 加载器链：
  获取 Java → 补全文件（`DlClientFix`）→ 获取启动参数（`McLaunchArgumentMain`）→ 解压 Natives（`McLaunchNatives`）→ 预启动处理 → 启动进程（`McLaunchRun`）→ 等待窗口。
- 启动参数：JVM 参数 → mainClass → 游戏参数；所有 `${...}` 占位符统一替换（`McLaunchArgumentsReplace`）。
- 旧版（无 `arguments.jvm`）用经典参数：`-XX:HeapDumpPath=...`、`-Djava.library.path=${natives_directory}`、`-cp ${classpath}`。
- 进程：`java.exe`（非 javaw），设置 `appdata` 环境变量指向 .minecraft，工作目录=实例目录；输出编码按 Java 版本选择（UTF-8/COMPAT/GBK）。
- Java 选择：`GetJavaRequirement`（1.20.5+→21，1.18pre2+→17，1.17+→16，1.12+→8），支持自动下载运行时。

## 下载源（Modules/Minecraft/ModDownload.vb）
- `DlSourceOrder`：默认**镜像优先**、官方兜底；`DlPreferMojang` 由官方版本列表测速决定（<4s 才官方优先）。
- 分类镜像路径：Assets→`/assets`、Libraries→`/maven`、`/libraries`（fabric/forge/neoforge 库仅镜像）、Launcher/Meta→镜像根。
- Modrinth/CurseForge → `mod.mcimirror.top` 镜像。
- 下载线程默认 64（`ToolDownloadThread=63`），速度默认不限。

## 其他要点
- Authlib-Injector（第三方认证）：`-javaagent:…\authlib-injector.jar=<server>` + `-Dauthlibinjector.side=client` + prefetch。
- Natives 每次启动都解压（按文件名+大小判断是否已解压），多余文件删除。
- `SplitJavaArguments`/`DeduplicateJavaArguments`：按引号切分参数，游戏参数键值对后者覆盖前者（--width 等）。
