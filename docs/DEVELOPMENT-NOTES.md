# PrismPanel 开发笔记（WinApp 国际版启动器 / 构建 / 已知坑）

> 本文档汇总本项目已知的开发信息，便于测试与排查。最后更新：2026-08-23。

## 项目结构
- `panel/`：Go 管理端（MySQL，含 SQLite 回退 `modernc.org/sqlite` + `toSQLite` 翻译器）。
- `daemon/`：Go 守护进程（文件/控制台/部署/防火墙，WebSocket 控制台）。
- `winapp/`：Go + Wails v2 的 Windows 客户端（国际版 MC 启动器、面板桥接）。
- `frontend/`：Vue3 + Element Plus + lucide 图标，CSS 变量主题（`--app-surface` 等）。
- `prism-plugin/`：Java 插件。
- `EG/Plain Craft Launcher 2/`：PCL2 官方源码（只读参考，见 `docs/PCL2参考笔记.md`）。
- `build/`：build-all.bat 输出目录（windows-amd64/…/winapp/PrismPanel.exe）。

## 构建环境与命令
- Go：`C:\Program Files\Go\bin\go.exe`（1.27）；Node：`C:\Program Files\nodejs\npm.cmd`（node 不在 PATH 时需手动加）。
- **GOPROXY 必须设 `https://goproxy.cn,direct`**（proxy.golang.org 国内不可达）。
- 全量构建：`build-all.bat windows`（前端 npm build → panel/daemon/WinApp → 嵌入 dist）。
- 前端单独构建：`frontend` 目录下 `npm run build`。
- 验证：`go build ./...`、`go vet ./...`；`go test ./internal/game/` 偶发被安全软件拦截（见已知坑）。
- `.bat` 必须 CRLF + 纯 ASCII（cmd 对 LF/UTF-8 中文解析错乱）。

## WinApp 国际版启动器功能清单
- 版本：下载安装（多源镜像）、Fabric 安装（"装 Fabric"独立入口）、删除（带重试）、每版本独立 `.minecraft`（`minecraft/<版本>/.minecraft`，`PRISMPANEL_MC_DIR` 覆盖根目录）。
- 启动：离线（UUIDv3）/微软设备码/第三方（authlib-injector）；启动版本可每版本指定（UseFabric/LaunchVersion）；自动选 Java（系统扫描→自动下载运行时）。
- Mod 管理：启停（`.disabled` 后缀重命名）、删除、打开目录、Modrinth 搜索/安装。
- 设置（总设置页 `/settings`，WinApp 专属）：并发数(1-64 默认16)、镜像(auto/bmclapi/off/自定义)、游戏目录、默认 Java、默认内存、**开发者模式**。
- 开发者模式：`minecraft/dev-mode.log` 记录所有操作与反馈（前端 `callWinApp` 统一拦截 + 后端 devOp），总设置页可查看/清空/打开。
- 下载队列：`prism:mc-download` 事件实时推送，最多 3 个版本并行，可取消/移除/清空；资源文件每 50 个上报「资源文件 X/Y」。

## 下载系统（对齐 PCL2）
- `mcCandidateURLs` 候选源顺序：默认镜像优先、官方兜底；官方源测速 <4s 才官方优先（`mcSourcePrefersOfficial`）。
- 源健康记忆（`mcHostHealth*`）：网络失败主机跳过、慢速主机降级末尾（10 分钟）。
- 镜像映射：root/maven/libraries/assets/fabric-meta；BMCLAPI `https://bmclapi2.bangbang93.com/`。
- Modrinth 走 `mod.mcimirror.top` 兜底镜像。
- 超时：拨号 15s / 响应头 30s / 整体 60s；`.part` 完整文件直接复用改名。

## 启动参数（对齐 PCL2）
- 版本 JSON `arguments.jvm` 占位符必须全部替换（`${classpath}` 等），否则 Java 报"找不到主类"。
- Java 要求以版本 JSON `javaVersion.majorVersion/component` 为准（26.2 → 25 / java-runtime-epsilon）。
- 进程环境变量 `APPDATA/appdata` 指向游戏目录；`java.exe`（非 javaw）。

## 已知坑
- **不要用 PowerShell `Get-Content -Raw`/`Set-Content` 改 Go 源文件**（按 GBK 读 UTF-8 → 中文乱码吞换行，曾损坏 app.go 等）。编辑一律用 write/edit 工具（UTF-8 无 BOM）。
- `go test` 偶发被安全软件拦截（"An Application Control policy has blocked this file"）；可用 `go vet` 验证编译，或 `go test -c -o <其他目录>` 后直接运行测试二进制。
- 新构建的 exe 未签名（build-all 会覆盖旧签名）；如需签名以管理员运行 `C:\Users\13602\AppData\Local\Temp\opencode\resign.ps1`（证书 `CN=PrismPanel Dev`，指纹 `9B076FEFF20C03AC2CB48A3E4F1181AE1F3EC8C9`）。
- 游戏日志在 `%LOCALAPPDATA%\PrismPanel\game-cache\logs\mc-*.log`；开发者日志在 `minecraft\dev-mode.log`。
- 启动失败排查：先看 mc-*.log 的 JVM 报错（如 `Unrecognized option: --sun-misc-unsafe-memory-access=allow` = Java 版本过低）。
