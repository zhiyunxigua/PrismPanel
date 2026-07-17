# PrismPanel Windows 客户端

WinApp 使用 Wails v2 和仓库根目录的 Vue 前端。普通 API 通过本地回环代理连接远程 Panel；Panel 签发短期票据后，文件传输和控制台日志可以直接连接 daemon。

## 首次启动

首次启动没有保存的 Panel 地址时，前端只显示“连接面板”页面。地址通过 /api/v1/auth/status 验证成功后保存到：

    %AppData%PrismPanelsettings.json

随后本地代理启动，前端进入现有登录流程。登录页和用户菜单中的“面板地址”入口只在 WinApp 显示。

## 连接边界

    普通 API       Vue -> 127.0.0.1 随机端口 -> 远程 Panel
    文件/日志数据  Vue -> daemon（Panel 短期票据）

本地代理在 Go 内存中保存远程 Panel Cookie，WebView 只持有随机本地会话标识。该标识不会发送到 daemon。

## 构建

    cd "winapp"
    powershell -NoProfile -ExecutionPolicy Bypass -File "scripts/build-frontend.ps1"
    go build -tags "desktop,production" -ldflags "-H windowsgui" -o "PrismPanel.exe" .

wails.json 也配置了同一套前端构建流程；安装 Wails CLI 后可直接使用 wails dev 或 wails build。