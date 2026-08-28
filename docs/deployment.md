# PrismPanel 部署说明

本文说明面板、守护进程和独立会话管理器 prism-sessiond 的部署与连接方式。

三个进程是独立软件，不要互相作为子进程启动：

```text
浏览器 / WinApp
    -> 面板 prism-panel
        -> 守护进程 prism-daemon（管理 WebSocket）
            -> 会话管理器 prism-sessiond（本机 socket + token）
                -> Minecraft / 代理服进程
```

- 面板负责登录、权限、审计、节点管理和请求转发。
- 守护进程负责节点上的服务器配置、文件、控制台和生命周期。
- prism-sessiond 负责真正拉起、持有和恢复游戏进程。守护进程重启时，正在运行的服务器继续留在 prism-sessiond 里。

## 1. 构建产物

在仓库根目录执行：

```bat
build.bat
```

或指定版本发布：

```bat
build-release.bat 0.0.1
```

每个目标平台会生成：

```text
dist/<os>-<arch>/
  panel/prism-panel[.exe]
  daemon/prism-daemon[.exe]
  sessiond/prism-sessiond[.exe]
  sessiond/prism-session[.exe]
  frontend/
```

prism-session 只是前台客户端，用来 SSH/本机手动查看和进入会话，不是守护进程的附属进程。

## 2. 部署面板

面板需要：

- Go 构建好的 prism-panel
- MySQL 8.0+
- 前端静态目录 frontend/dist

先创建数据库：

```sql
CREATE DATABASE prismpanel
  CHARACTER SET utf8mb4
  COLLATE utf8mb4_unicode_ci;
```

把 prism-panel 和前端目录放到同一台机器，例如：

```text
/www/PrismPanel/panel/prism-panel
/www/PrismPanel/frontend/dist
```

首次启动：

```bash
cd /www/PrismPanel/panel
./prism-panel --config /www/PrismPanel/panel/panel.yaml
```

首次运行会生成：

- panel.yaml
- data/master.key

master.key 用来加密节点长期令牌，丢失后已保存的节点令牌无法解密。

最小配置示例：

```yaml
server:
  listen: 127.0.0.1
  port: 8080

database:
  url: 127.0.0.1:3306
  username: root
  password: replace-with-your-password
  name: prismpanel
  table_prefix: prism_

frontend:
  directory: ../frontend/dist
```

如果前面还有 Nginx / 宝塔反代，把面板继续监听本机，由反代对外提供 HTTPS。

数据库中还没有用户时，打开登录页并输入用户名和密码。第一组通过校验的凭据会原子创建首位超级管理员并立即登录。系统不提供公开注册，也不创建默认账号。

## 3. 部署 prism-sessiond

prism-sessiond 必须先于守护进程部署，并且注册成开机自启的系统服务。它和守护进程使用本机协议通信，不是守护进程的子进程。

### Linux

把二进制放到固定目录，例如 /usr/local/bin/prism-sessiond，然后安装服务：

```bash
sudo install -m 0755 prism-sessiond /usr/local/bin/prism-sessiond
sudo /usr/local/bin/prism-sessiond --config /etc/prism-sessiond/sessiond.yaml install
```

install 会：

1. 写入 /etc/systemd/system/prism-sessiond.service
2. 执行 systemctl daemon-reload
3. 执行 systemctl enable --now prism-sessiond

因此不需要再手动执行一次 prism-sessiond 来启动服务。

默认配置：

```yaml
listen: /run/prism-sessiond/session.sock
state_dir: /var/lib/prism-sessiond
token_file: /etc/prism-sessiond/token
orphan_timeout_seconds: 180
```

首次启动会自动创建配置和 token。token 文件权限是 0600，只给本机守护进程和前台客户端读取。

查看状态：

```bash
systemctl status prism-sessiond
journalctl -u prism-sessiond -f
```

卸载：

```bash
sudo /usr/local/bin/prism-sessiond uninstall
```

### Windows

把 prism-sessiond.exe 放到固定目录后执行：

```bat
prism-sessiond.exe --config "%ProgramData%\PrismPanel\sessiond\sessiond.yaml" install
```

它会创建并启动名为 prism-sessiond 的 Windows 服务，开机自动启动。

默认路径：

```text
%ProgramData%\PrismPanel\sessiond\sessiond.yaml
%ProgramData%\PrismPanel\sessiond\session.sock
%ProgramData%\PrismPanel\sessiond\token
%ProgramData%\PrismPanel\sessiond\state
```

Windows 上的 session.sock 实际是本机 TCP 端口文件，协议与 Linux Unix socket 相同。

### 前台手动管理

服务启动后，管理员可以再开一个前台客户端：

```bash
prism-session
```

或指定配置：

```bash
prism-session --config /etc/prism-sessiond/sessiond.yaml
```

前台布局是一列服务器列表：

- 上下方向键选择
- 回车进入该会话终端
- Esc 退出当前会话，回到列表
- q 退出前台

只列出会话、不进入 TUI：

```bash
prism-session list
```

## 4. 部署守护进程

守护进程部署在运行 Minecraft / 代理服的节点上。它只连接本机 prism-sessiond，不再自己拉起游戏进程。

### Linux 示例

```bash
mkdir -p /www/PrismPanel/daemon
cd /www/PrismPanel/daemon
./prism-daemon --config /www/PrismPanel/daemon/daemon.yaml
```

宝塔 Go 项目也可以继续用现有启动脚本，例如：

```bash
cd /www/PrismPanel/daemon
nohup /www/PrismPanel/daemon/prism-daemon &>> /www/wwwlogs/go/prism_daemon.log &
echo $! > /var/tmp/gopids/prism_daemon.pid
```

首次启动会生成：

- daemon.yaml
- data/secret.json

查看节点令牌：

```bash
./prism-daemon --show-secret
```

重置令牌：

```bash
./prism-daemon --reset-secret
```

重置令牌不会改变稳定节点 ID，但所有使用旧令牌的面板都会认证失败。

最小配置示例：

```yaml
server:
  listen: 0.0.0.0
  port: 24444
  public_url: https://node.example.com:24444

storage:
  data_dir: data

process:
  console_buffer_lines: 2000
  shutdown_timeout_seconds: 90
  session_orphan_timeout_seconds: 180
  session_socket: /run/prism-sessiond/session.sock
  session_token_file: /etc/prism-sessiond/token
```

Windows 默认连接：

```yaml
process:
  session_socket: C:\ProgramData\PrismPanel\sessiond\session.sock
  session_token_file: C:\ProgramData\PrismPanel\sessiond\token
```

这两个路径必须和 prism-sessiond 的 listen、token_file 一致。守护进程启动服务器时，会向 prism-sessiond 发送 session.start；关闭守护进程时只会断开控制连接，不会主动停止游戏进程。

如果 prism-sessiond 未启动，守护进程可以自己起来，但启动服务器会失败，并提示无法连接会话管理器。

## 5. 面板连接守护进程

登录面板后进入“节点”，填写：

- 节点名称
- 完整 HTTP 或 HTTPS 连接 URL，例如 http://192.168.1.20:24444
- daemon 节点令牌
- 可选公网 URL

连接关系是多对多：

- 一个面板可以连接多个守护进程
- 一个守护进程也允许被多个面板同时连接

面板保存节点后会持续重连。连接测试是可选操作，离线或令牌错误的节点也可以先保存。

如果守护进程前面有 Nginx / 宝塔反代，需要把反代的真实来源 CIDR 加到 daemon 的 security.trusted_proxy_cidrs。不要使用 0.0.0.0/0。

## 6. 手动更新守护进程

更新守护进程时，按这个顺序：

1. 确认 prism-sessiond 仍在运行。
2. 替换 prism-daemon 二进制。
3. 重启守护进程，例如宝塔停止后再启动，或 kill 后再执行原来的启动命令。

守护进程收到关闭信号后只会断开会话控制连接，不会让 prism-sessiond 关闭游戏进程。新守护进程启动后会向 prism-sessiond 查询现有会话并重新接管。

注意：

- 只重启守护进程，服务器应继续运行。
- 不要在更新守护进程时停止 prism-sessiond。
- 如果 prism-sessiond 自己被停掉，并且超时后没有守护进程重新连接，它会按 orphan_timeout_seconds 结束无人认领的会话。

更新 prism-sessiond 是另一次操作：先停服务、替换二进制、再启动服务。这个过程中游戏进程不会自动迁移，因此不要把它和守护进程热更新混在一起。

## 7. 推荐启动顺序

1. 启动 MySQL。
2. 启动 prism-sessiond 系统服务。
3. 启动 prism-daemon。
4. 启动 prism-panel。
5. 在面板中添加节点并填写守护进程 URL 和令牌。

本机自检：

```bash
prism-session list
./prism-daemon --show-secret
```

如果 prism-session list 能列出会话，说明会话管理器正常；如果面板节点显示在线，说明面板已经连上守护进程。
