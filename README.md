# CampusLink

CampusLink 是一个校园网自动登录工具。实现语言为 Go，产物是单文件二进制程序，不依赖脚本运行时、包管理器或虚拟环境。默认连接 `https://nap.cug.edu.cn`，通过系统 DNS 解析域名后连接。

## 认证与连接

程序通过账号认证 API 登录：自动获取客户端 IP 和 NAS ID，获取 CSRF token 与会话 Cookie，检查在线状态，再依次通过表单 POST 调用 `/api/account/check` 和 `/api/account/login`。已在线时直接成功返回；验证码或强制改密要求会提示前往浏览器处理。

默认通过系统 DNS 解析 `nap.cug.edu.cn`，并使用 HTTPS 连接和校验证书。登录前检查解析结果：

- 包含 `192.168.167.72`：正常继续。
- 不包含该 IP：向标准错误输出实际解析结果、预期 IP 和排查建议，仍通过域名连接，不强制替换 IP。`--json` 的标准输出保持为 JSON。
- DNS 查询失败或没有返回地址：报错退出，不回退到固定 IP。DNS 查询受 `--timeout` 限制。

`192.168.167.72` 仅用于提醒，不是硬编码连接地址。默认域名不使用环境变量 HTTP 代理；VPN/TUN 等系统级代理仍可能影响 DNS 和连接。自定义 `--base-url` 不执行校园网域名的 IP 对比。

## 凭据读取

- 用户名：`CAMPUSLINK_USERNAME` 提供初始值，`-u` / `--username` 覆盖它。
- 密码：`CAMPUSLINK_PASSWORD` 提供初始值，`-p` / `--password` 覆盖它。
- 命令行参数重复时最后一个生效；空字符串也会覆盖环境变量。
- `--password-stdin` 仅在上述解析后的密码为空时读取标准输入第一行，否则报冲突。读取时去掉行尾换行，保留其他空格。
- 最终账号或密码为空会报错；不自动加载 `.env`、配置文件或钥匙串，也不弹出交互输入提示。

## 下载和安装

GitHub Actions 会为以下目标系统编译二进制产物：

| 系统 | 架构 | Artifact |
| --- | --- | --- |
| Linux | x64 / amd64 | `campuslink-linux-amd64.tar.gz` |
| Windows | x64 / amd64 | `campuslink-windows-amd64.zip` |
| macOS | arm64 / Apple Silicon | `campuslink-darwin-arm64.tar.gz` |

从 [GitHub Releases](https://github.com/cugcs632/CampusLink/releases/latest) 下载对应系统的压缩包。解压后得到：

- Windows: `campuslink.exe`
- Linux/macOS: `campuslink`

推荐安装位置：

| 系统 | 推荐路径 |
| --- | --- |
| Windows | `%LOCALAPPDATA%\CampusLink\campuslink.exe` |
| Linux | `$HOME/.local/bin/campuslink` |
| macOS | `$HOME/.local/bin/campuslink` |

Windows PowerShell:

```powershell
$InstallDir = "$env:LOCALAPPDATA\CampusLink"
New-Item -ItemType Directory -Force -Path $InstallDir
Expand-Archive .\campuslink-windows-amd64.zip -DestinationPath . -Force
Copy-Item .\campuslink-windows-amd64\campuslink.exe "$InstallDir\campuslink.exe" -Force
```

Linux:

```bash
mkdir -p "$HOME/.local/bin"
tar -xzf campuslink-linux-amd64.tar.gz
install -m 0755 campuslink-linux-amd64/campuslink "$HOME/.local/bin/campuslink"
```

macOS:

```bash
mkdir -p "$HOME/.local/bin"
tar -xzf campuslink-darwin-arm64.tar.gz
install -m 0755 campuslink-darwin-arm64/campuslink "$HOME/.local/bin/campuslink"
```

如果 `campuslink` 命令无法直接找到，可以使用完整路径 `$HOME/.local/bin/campuslink`，或把 `$HOME/.local/bin` 加入 `PATH`。

也可以从源码本地编译安装：

```bash
go build -trimpath -ldflags="-s -w" -o "$HOME/.local/bin/campuslink" ./cmd/campuslink
```

## 快速使用

先手动验证账号、密码和认证参数：

```bash
"$HOME/.local/bin/campuslink" -u "学号" -p "密码"
```

Windows PowerShell:

```powershell
& "$env:LOCALAPPDATA\CampusLink\campuslink.exe" -u "学号" -p "密码"
```

成功时普通模式会输出：

```text
login ok
```

需要查看网关原始响应时再加 `--json`：

```bash
"$HOME/.local/bin/campuslink" -u "学号" -p "密码" --json
```

也可以使用环境变量，避免把密码写进命令历史：

```bash
export CAMPUSLINK_USERNAME="学号"
export CAMPUSLINK_PASSWORD="密码"
"$HOME/.local/bin/campuslink"
```

还可以从标准输入读取密码，便于对接密码管理器或其他不会把密码放进进程参数的工具：

```bash
read -rsp "Campus network password: " CAMPUSLINK_PASSWORD
printf '%s\n' "$CAMPUSLINK_PASSWORD" | env -u CAMPUSLINK_PASSWORD "$HOME/.local/bin/campuslink" -u "学号" --password-stdin
unset CAMPUSLINK_PASSWORD
```

查看当前版本：

```bash
"$HOME/.local/bin/campuslink" --version
```

Release 和 GitHub Actions 构建会同时显示版本号与 commit，例如 `campuslink v1.2.3 (commit abcdef123456)`。本地源码构建会读取 Go 嵌入的 Git 信息；工作区存在未提交改动时，commit 后会附加 `-dirty`。

可配置项：

| 环境变量 | 命令行参数 | 默认值 | 说明 |
| --- | --- | --- | --- |
| `CAMPUSLINK_USERNAME` | `-u, --username` | 无 | 校园网账号 |
| `CAMPUSLINK_PASSWORD` | `-p, --password` | 无 | 校园网密码 |
| 无 | `--password-stdin` | 关闭 | 从标准输入读取密码，不能与其他密码来源同时使用 |
| `CAMPUSLINK_IP` | `--ip` | 自动发现 | 自动发现失败时手动指定客户端 IP |
| `CAMPUSLINK_BASE_URL` | `--base-url` | `https://nap.cug.edu.cn` | 网关基础 URL，支持 HTTP/HTTPS；默认通过域名连接 |
| `CAMPUSLINK_NAS_ID` | `--nas-id` | 自动发现 | 接入设备 ID；自动发现失败时可手动指定 |
| `CAMPUSLINK_ISP` | `--isp` | 不发送 | 可选运营商 ID；按网关要求指定 |
| `CAMPUSLINK_TIMEOUT` | `--timeout` | `8` | HTTP 超时时间，单位秒，有效范围 1–3600；非法配置会直接报错 |

默认使用 HTTPS；也可以明确指定：

```bash
"$HOME/.local/bin/campuslink" --base-url "https://nap.cug.edu.cn"
```

没有协议前缀的 `--base-url` 值按 HTTPS 处理。自定义网关使用所提供的地址，不会把 IP 改写为域名。客户端会校验 URL、手动指定的 IP、NAS ID 和超时范围；网关响应最多读取 1 MiB，避免异常响应占用过多内存。

## 自动连接方案

推荐方案是：系统启动或用户登录后立即执行一次，然后每 5 分钟重试一次。CampusLink 把“已经在线”视为成功，所以重复执行是安全的；这种方式也能覆盖 Wi-Fi 切换、睡眠恢复、网关踢线等情况。

下面的示例使用上文的推荐安装路径。如果你安装到了其他位置，把服务配置里的程序路径替换成自己的实际路径。

### Windows

Windows 推荐使用任务计划程序。先在 PowerShell 中保存用户级环境变量：

```powershell
[Environment]::SetEnvironmentVariable("CAMPUSLINK_USERNAME", "学号", "User")
[Environment]::SetEnvironmentVariable("CAMPUSLINK_PASSWORD", "密码", "User")
```

关闭并重新打开 PowerShell，确认能手动登录：

```powershell
& "$env:LOCALAPPDATA\CampusLink\campuslink.exe"
```

创建登录后执行一次的任务：

```powershell
$CampusLink = "$env:LOCALAPPDATA\CampusLink\campuslink.exe"
schtasks /Create /TN "CampusLink Login" /SC ONLOGON /DELAY 0001:00 /TR "`"$CampusLink`"" /RL LIMITED /F
```

创建每 5 分钟补偿重试的任务：

```powershell
$CampusLink = "$env:LOCALAPPDATA\CampusLink\campuslink.exe"
schtasks /Create /TN "CampusLink Login Retry" /SC MINUTE /MO 5 /TR "`"$CampusLink`"" /RL LIMITED /F
```

立即测试任务：

```powershell
schtasks /Run /TN "CampusLink Login"
```

删除任务：

```powershell
schtasks /Delete /TN "CampusLink Login" /F
schtasks /Delete /TN "CampusLink Login Retry" /F
```

### Linux

Linux 推荐使用 systemd 用户服务和 timer，不需要 root，适合大多数桌面发行版。

保存账号配置：

```bash
mkdir -p ~/.config/campuslink
cat > ~/.config/campuslink/env <<'EOF'
CAMPUSLINK_USERNAME=学号
CAMPUSLINK_PASSWORD=密码
EOF
chmod 600 ~/.config/campuslink/env
```

创建服务文件 `~/.config/systemd/user/campuslink-login.service`：

```ini
[Unit]
Description=CampusLink login

[Service]
Type=oneshot
EnvironmentFile=%h/.config/campuslink/env
ExecStart=%h/.local/bin/campuslink
```

创建定时器 `~/.config/systemd/user/campuslink-login.timer`：

```ini
[Unit]
Description=Run CampusLink login periodically

[Timer]
OnBootSec=1min
OnUnitActiveSec=5min
Unit=campuslink-login.service

[Install]
WantedBy=timers.target
```

启用并立即启动：

```bash
systemctl --user daemon-reload
systemctl --user enable --now campuslink-login.timer
systemctl --user start campuslink-login.service
```

查看状态和日志：

```bash
systemctl --user status campuslink-login.service
journalctl --user -u campuslink-login.service -n 50
```

如果希望未登录桌面会话时也能运行用户服务，可以启用 linger：

```bash
sudo loginctl enable-linger "$USER"
```

如果你的 Linux 使用 NetworkManager，并且更希望“网络连上就立刻登录”，可以额外做 dispatcher 脚本；但这需要 root，并且不同发行版路径略有差异。systemd timer 更通用，也更容易排错。

### macOS

macOS 推荐使用 LaunchAgent。先创建日志目录：

```bash
mkdir -p ~/Library/Logs/CampusLink
```

创建 `~/Library/LaunchAgents/io.github.campuslink.login.plist`。下面的命令会把当前用户的 `$HOME` 写入 plist，避免手动替换 `/Users/...` 路径：

```bash
cat > ~/Library/LaunchAgents/io.github.campuslink.login.plist <<EOF
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN"
  "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key>
  <string>io.github.campuslink.login</string>

  <key>ProgramArguments</key>
  <array>
    <string>$HOME/.local/bin/campuslink</string>
  </array>

  <key>EnvironmentVariables</key>
  <dict>
    <key>CAMPUSLINK_USERNAME</key>
    <string>学号</string>
    <key>CAMPUSLINK_PASSWORD</key>
    <string>密码</string>
  </dict>

  <key>RunAtLoad</key>
  <true/>
  <key>StartInterval</key>
  <integer>300</integer>

  <key>StandardOutPath</key>
  <string>$HOME/Library/Logs/CampusLink/stdout.log</string>
  <key>StandardErrorPath</key>
  <string>$HOME/Library/Logs/CampusLink/stderr.log</string>
</dict>
</plist>
EOF
```

设置权限、加载并立即执行：

```bash
chmod 600 ~/Library/LaunchAgents/io.github.campuslink.login.plist
launchctl bootstrap "gui/$(id -u)" ~/Library/LaunchAgents/io.github.campuslink.login.plist
launchctl kickstart -k "gui/$(id -u)/io.github.campuslink.login"
```

查看日志：

```bash
tail -n 50 ~/Library/Logs/CampusLink/stdout.log
tail -n 50 ~/Library/Logs/CampusLink/stderr.log
```

卸载：

```bash
launchctl bootout "gui/$(id -u)" ~/Library/LaunchAgents/io.github.campuslink.login.plist
```

## GitHub CI

- `go test ./...` 验证重定向发现、CSRF/Cookie、表单登录、在线状态、验证码和强制改密、DNS 提示和 CLI 行为。
- `go vet ./...` 做基础静态检查。
- `go test -race ./...` 验证并发访问安全性；模拟网关测试覆盖认证流程、DNS 检查、超时、响应上限、错误脱敏和跨主机重定向拒绝。
- `.github/workflows/build.yml` 用于普通 push、PR 和手动触发，交叉编译 Linux x64、Windows x64、macOS arm64，并上传 Actions artifacts。
- `.github/workflows/release.yml` 只负责发版。推送 `v*` tag 会自动创建 GitHub Release，并上传三个系统的压缩包。
- Release workflow 也支持手动触发，输入已有 tag 后会重新构建并补发对应 Release assets。

## 安全说明

- 不要把账号密码提交到 Git。
- 命令行 `-p, --password` 可能出现在 shell 历史和进程列表中；交互使用优先选择环境变量，接入密码管理器时优先选择 `--password-stdin`。
- Windows 用户级环境变量、Linux `EnvironmentFile`、macOS plist 都会在本机保存明文密码；请确保电脑账号本身有登录密码，并限制配置文件权限。
- macOS plist 中的账号或密码如果包含 `&`、`<`、`>` 等字符，需要进行 XML 转义，否则 plist 无法加载。
- 网络错误只记录请求路径和底层原因，不记录包含认证参数的完整 URL；`--json` 会主动输出网关原始响应，请避免把调试输出发送到公共日志。
- 如果需要更强的凭据保护，后续可以扩展为从 Windows Credential Manager、Linux Secret Service 或 macOS Keychain 读取密码。

## 故障排查

- `cannot find valid client IP from portal redirect; pass --ip`：认证页重定向没有返回有效 IP。先确认已连接校园网，必要时重新向 DHCP 申请 IP。重新获取地址后再运行 CampusLink；如果仍失败，可以使用 `--ip` 或 `CAMPUSLINK_IP` 手动指定。

  Windows PowerShell:

  ```powershell
  ipconfig /release
  ipconfig /renew
  ```

  Linux NetworkManager:

  ```bash
  nmcli networking off
  nmcli networking on
  ```

  macOS:

  ```bash
  sudo ifconfig en0 down
  sudo ifconfig en0 up
  ```

- `cannot find valid NAS ID from portal redirect; pass --nas-id`：在浏览器打开认证页，从地址栏读取 `nasId` 后通过 `--nas-id` 或 `CAMPUSLINK_NAS_ID` 指定。若 IP 和 NAS ID 均手动指定，程序会跳过重定向发现。
- `captcha required` / `password change required`：在浏览器打开认证页完成验证码或修改密码，再更新本地密码配置。
- 无法登录时，先排查代理问题：关闭系统代理、浏览器代理、VPN、TUN 模式或透明代理，确认校园网网关流量没有被代理接管。
- DNS 警告：检查提示中的实际解析地址。如果不包含 `192.168.167.72`，确认已连接校园网、DNS 设置正确，检查 VPN/TUN 的 DNS 接管，并确认学校是否再次调整网关地址。该提示不阻止登录，也不会改变域名连接方式。
- `cannot resolve nap.cug.edu.cn` / `DNS returned no addresses`：域名解析失败，请检查校园网连接和 DNS 设置。
- `portal request ... failed`：确认浏览器可访问 `https://nap.cug.edu.cn`，检查网络连接和证书错误。

- `login failed`：使用 `--json` 查看网关返回的原始错误，再确认账号、密码、NAS ID 和学校网关地址。

## 许可证

CampusLink 使用 [MIT License](LICENSE)。
