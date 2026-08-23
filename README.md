# PrismPanel（棱镜面板）

面向 **Minecraft 多实例、多 Linux 节点** 场景的分布式服务器管理面板。覆盖实例部署、进程启停、控制台、性能监控、插件与资源管理、OP 管理、文件管理、定时任务、防火墙与网络游戏在线人数监控等能力。

当前版本：`v0.0.1`（见 `VERSION`）

## 功能特性

- **多节点管理**：守护进程部署于每台 Linux 节点，面板统一纳管，支持节点分组与多选批量操作
- **实例全生命周期**：创建、启动、停止、重启、强制结束（含超时强制语义）、部署任务与实时部署日志
- **控制台与监控**：控制台命令经面板鉴权审计后转发；日志经短期票据直连节点；实时采集 TPS、MSPT、JVM 内存、在线玩家、进程树 CPU/RSS 等指标
- **插件中心**：插件仓库扫描/导入、描述符解析、实例上传、配置同步与部署
- **OP 管理**：面板维护全局 OP 名单，Spigot/Paper 插件拦截并修复 OP 漂移，支持代理端（Bungee/Velocity）场景
- **文件管理**：文件树、上传下载（分片）、在线编辑（CodeMirror）、复制/移动/重命名/删除、压缩归档，全部带版本冲突与审计
- **用户与权限**：多用户、角色权限、操作审计、登录限流、会话管理
- **网络游戏支持**：除 Minecraft 外的网络游戏实例、在线人数监控、防火墙（白名单）规则下发
- **Windows 桌面客户端**：基于 Wails 的 WinApp，复用同一前端，支持一键连接远程面板与自动更新

## 系统架构

```text
┌──────────────────────────────────────────┐
│ Web 浏览器                Windows 桌面应用  │
│ Vue 3 + Vite + Element Plus  Wails + Vue │
└─────────────────────┬────────────────────┘
                      │ HTTPS / WebSocket
┌─────────────────────▼────────────────────┐
│              panel（面板后端，Go）          │
│ 用户权限 · 节点注册 · 指令路由 · 审计 · 插件中心 │
└──────┬──────────────────────────┬────────┘
       │ 原生 WebSocket（面板主动连接）  │ 短期票据（直连能力）
┌──────▼──────┐              ┌──────▼──────┐
│ daemon 节点 A │              │ daemon 节点 B │
│ Linux · 单二进制 │              │ Linux · 单二进制 │
└──┬───┬───┬───┘              └──┬───┬───┬───┘
  MC  MC  MC                    MC  MC  MC
   │   │                         │   │
 prism-plugin（Spigot/Paper/Bungee/Velocity）
 上报 TPS · 玩家 · 插件状态 · OP 修复
```

- 浏览器/桌面端**不直连守护进程**；所有会改变状态的请求一律经面板鉴权与审计
- 控制台日志、部署日志、文件传输通过面板签发的**短期临时凭证**直连节点，减轻面板转发压力
- 守护进程**节点主动接入**，管理端口不对公网开放；使用主密钥认证 WebSocket，临时任务凭证处理直连

## 目录结构

```
PrismPanel/
├── frontend/        # 前端（Vue 3 + Vite + Element Plus + xterm + CodeMirror）
│                     #   Web 浏览器与 Windows 桌面端复用同一套 UI
├── winapp/          # Windows 桌面客户端（Wails v2 + Go）
│                     #   薄壳层：本地回环代理、凭证存储（DPAPI）、游戏启动、自动更新
├── panel/           # 面板后端（Go）
│                     #   REST API + WebSocket，用户/节点/实例/插件/审计等核心业务
├── daemon/          # 节点守护进程（Go）
│                     #   部署于 Linux 节点：进程/控制台/文件/部署/插件/防火墙，零依赖单二进制
├── prism-plugin/    # Minecraft 子服插件（Java，Gradle 多模块）
│                     #   core + spigot + bungee + velocity，经环境变量注入连接 daemon
├── docs/            # 项目文档（架构设计、协议、数据模型、运行说明）
├── build.bat        # 一键构建（前端 + Windows/Linux 多平台产物）
├── build-target.bat # 单平台交叉构建脚本
├── build-release.bat# 发布构建脚本
└── VERSION          # 版本号
```

## 技术栈

| 组件 | 技术 | 说明 |
|---|---|---|
| 前端 | Vue 3 / Vite / Element Plus / xterm / CodeMirror | Web 与桌面端共用 |
| 桌面端 | Wails v2 / Go | Windows 容器与本地系统适配 |
| 面板后端 | Go | REST + WebSocket，可配置 MySQL/SQLite 存储 |
| 节点守护进程 | Go | Linux 优先，单一可执行文件，原子 JSON 本地存储 |
| 子服插件 | Java 17 / Spigot / Paper / Bungee / Velocity | 游戏内数据上报与 OP 修复 |

## 构建

```bat
:: 一键构建全部模块（前端 + windows/amd64、linux/amd64、linux/arm64）
build.bat

:: 仅构建单个目标平台（见 build-target.bat 参数）
build-target.bat windows amd64 .exe
build-target.bat linux amd64
build-target.bat linux arm64
```

- 产物输出到 `dist/`，按平台分目录（git 已忽略）
- 前端构建需 Node.js 与 npm，Go 模块构建需 Go toolchain
- 桌面端单独构建见 `winapp/README.md`

## 文档

### 本次改造文档（2026-08：mod 支持 / 国际版监控 / WinApp 启动器）

- [改造总览](docs/改造总览.md)——三大改造与待办落地总览（入口）
- [mod 统一管理与配置同步](docs/mod统一管理与配置同步.md)——服主指南：fabric/forge 平台、config 同步、mod 管理、运行态上报
- [国际版人数监控](docs/国际版人数监控.md)——服主指南：手动输入 IP、自动采集、API
- [WinApp 启动器改进](docs/WinApp启动器改进.md)——客户端改进清单与构建
- [PCL2-winapp改进建议](docs/PCL2-winapp改进建议.md)——PCL2 源码逐项对比研究（29 条速览表，含组件映射勘误）
- [mod 运行态上报设计](docs/mod运行态上报设计.md)——Fabric mod 运行态上报方案
- [审查报告-2026](docs/审查报告-2026.md)——多轮全面审查 + 下载修复 + Fabric mod 编译验证

### 既有文档

- [守护进程设计](docs/守护进程设计.md)（协议、数据模型、通信规则）
- [面板设计](docs/面板设计.md)
- [网络白名单实现设计](docs/网络白名单实现设计.md)
- [代理服与插件多平台设计](docs/代理服与插件多平台设计.md)
- [WinApp 本地游戏启动设计](docs/WinApp本地游戏启动设计.md)
- [基础框架运行](docs/基础框架运行.md)
- [目录说明](目录说明.md)

## 测试

各 Go 模块（panel、daemon、winapp）与前端（`frontend/tests`）均附带单元测试：

```bash
cd panel && go test ./...     # 面板
cd daemon && go test ./...    # 守护进程
cd winapp && go test ./...    # 桌面端
```

## 设计参考

守护进程设计参考 MCSManager daemon 的节点监听、面板主动连接、主密钥认证、临时任务凭证等成熟思路，并结合本项目需要改用原生 WebSocket，扩展了镜像服统一模型、批量文件树、文件版本冲突、部署事务与取消/强制结束语义。
