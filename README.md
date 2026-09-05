# CampusLink

CampusLink 是一个校园网自动认证工具，默认通过 `https://nap.cug.edu.cn` 连接。Windows、macOS、Linux 使用相同的命令完成配置、登录、自动连接和故障排查。程序由 Go 编译为单文件，不需要 Python、Node.js 或其他脚本运行时；系统后台任务使用各平台自带的管理工具。

## 下载和安装

从 [GitHub Releases](https://github.com/cugcs632/CampusLink/releases/latest) 下载对应系统和架构的压缩包并解压：

| 系统 | 架构 | 压缩包 |
| --- | --- | --- |
| Windows | x64 / ARM64 | `campuslink-windows-amd64.zip` / `campuslink-windows-arm64.zip` |
| macOS | Apple Silicon / Intel | `campuslink-darwin-arm64.tar.gz` / `campuslink-darwin-amd64.tar.gz` |
| Linux | x64 / ARM64 | `campuslink-linux-amd64.tar.gz` / `campuslink-linux-arm64.tar.gz` |

上述产物由本仓库工作流构建；已发布版本的可用架构以其 Release assets 为准。

在解压目录打开终端，首次配置：

Windows PowerShell：

```powershell
.\campuslink.exe setup
```

macOS / Linux：

```bash
./campuslink setup
```

向导会要求输入账号和密码（输入密码时不回显），连接网关验证，保存配置，然后询问是否启用自动连接。设备已经在线时，网关状态检查无法验证新输入密码是否正确；首次离线登录时才能完成该验证。

启用自动连接时，程序会把自身复制到配置目录下的 `bin` 子目录，并显示安装后的完整路径。后台任务使用这份副本；解压目录随后可以移走。下文的 `campuslink` 可替换为该完整路径，也可将其所在目录加入 `PATH`。

从源码编译：

```bash
go build -trimpath -ldflags="-s -w" -o campuslink ./cmd/campuslink
```

## 常用命令

| 命令 | 用途 |
| --- | --- |
| `campuslink setup` | 配置或更新账号、密码，选择自动连接 |
| `campuslink login` | 立即认证；不带子命令时也执行登录 |
| `campuslink status` | 查询在线状态、后台任务状态、最近成功时间与暂停原因 |
| `campuslink autostart enable` | 安装或更新后台任务和程序副本，并恢复自动重试 |
| `campuslink autostart disable` | 移除后台任务，保留配置和凭据 |
| `campuslink logs` | 查看最近运行记录 |
| `campuslink doctor` | 检查配置、已保存凭据是否可读、后台任务、DNS 和 HTTPS 认证接口 |
| `campuslink --version` | 查看版本及 commit |

`status` 和 `doctor` 只查询认证接口，不提交密码；`doctor` 会尝试读取已保存凭据来检查其可用性，但不会输出密码。支持 `--json` 输出结构化结果：

```bash
campuslink status --json
campuslink doctor --json
campuslink login --json
```

`login --json` 输出网关原始响应，可能包含账号和会话信息，请勿直接公开。普通登录成功输出 `login ok`；失败返回非零退出码。`status` 在确认设备离线时仍正常退出；网络查询失败返回非零。`doctor` 发现凭据或后台任务访问问题也返回非零。

## 配置和凭据

配置默认保存在当前用户的系统配置目录：

| 系统 | 默认目录 |
| --- | --- |
| Windows | `%APPDATA%\CampusLink` |
| macOS | `~/Library/Application Support/CampusLink` |
| Linux | `$XDG_CONFIG_HOME/CampusLink`，未设置时为 `~/.config/CampusLink` |

`config.json` 保存账号、网关、NAS ID、超时和密码存储方式，不包含密码。可在所有命令之前指定其他配置目录：

```bash
campuslink --config-dir "/path/to/CampusLink" setup
campuslink --config-dir "/path/to/CampusLink" status
```

也可以设置 `CAMPUSLINK_CONFIG_DIR`。命令行目录优先于环境变量。每个系统用户使用一套自动连接任务；切换配置目录前先在原目录下执行 `autostart disable`。

### 密码存储

`setup` 默认使用系统凭据服务：

- Windows：Windows 凭据管理器。
- macOS：登录钥匙串，系统可能要求授权访问。
- Linux：Secret Service，需要可用的用户 D-Bus 会话及已解锁的凭据服务。

凭据服务不可用时会报错，不会自动降级为明文。无桌面 Linux 或其他需要文件存储的场景，可明确选择：

```bash
campuslink setup --storage file
```

这种方式把密码明文保存到配置目录中的独立 `password` 文件。Unix 目录权限为 `0700`，文件为 `0600`；Windows 配置目录使用限制为当前用户和 SYSTEM 的 ACL。系统管理员仍可能访问这些数据。

切换为钥匙串存储成功后，程序会删除本配置目录的明文密码文件。`autostart disable` 保留凭据；若要彻底清除，停用后台任务后删除配置目录，并在系统凭据管理界面删除对应的 `io.github.campuslink.*` 项。

### 临时覆盖与优先级

手动 `login` 按以下顺序读取配置：

1. 命令行参数。
2. 非空 `CAMPUSLINK_*` 环境变量。
3. 已保存配置及关联密码。
4. 内置默认值（账号密码没有默认值）。

同一命令行参数重复指定时最后一个生效；显式传入空字符串不会回退到已保存值。密码仅在账号和网关均匹配已保存配置时自动读取，避免把保存的密码发送给其他网关或用于其他账号。

`--password-stdin` 单独处理：如果命令行和环境变量解析后的密码非空，则报冲突；否则读取标准输入第一行，并跳过保存的密码。去掉行尾换行，保留其他空格。

```bash
campuslink login -u "学号" --password-stdin
```

输入完密码后换行即可；在普通终端使用此模式时输入可能回显，交互配置应优先使用 `setup` 的隐藏输入。它也适合由密码管理器通过管道提供密码。

自动连接始终使用保存的配置和密码，不依赖启动任务时继承的账号密码环境变量。修改长期使用的账号或密码请重新运行 `setup`。

| 环境变量 | `login` 参数 | 默认值 |
| --- | --- | --- |
| `CAMPUSLINK_USERNAME` | `-u` / `--username` | 保存的账号 |
| `CAMPUSLINK_PASSWORD` | `-p` / `--password` | 保存的密码 |
| 无 | `--password-stdin` | 关闭 |
| `CAMPUSLINK_BASE_URL` | `--base-url` | 保存的网关或 `https://nap.cug.edu.cn` |
| `CAMPUSLINK_IP` | `--ip` | 保存的客户端 IP 或自动发现 |
| `CAMPUSLINK_NAS_ID` | `--nas-id` | 保存的 NAS ID 或自动发现 |
| `CAMPUSLINK_ISP` | `--isp` | 保存的运营商 ID 或不发送 |
| `CAMPUSLINK_TIMEOUT` | `--timeout` | 保存的秒数或 `8`，范围 1–3600 |

`setup` 使用其参数和已有配置，不从登录用的环境变量导入凭据。支持 `--username`、`--base-url`、`--ip`、`--nas-id`、`--isp`、`--timeout`、`--storage`、`--password-stdin` 和 `--no-autostart`。不自动读取 `.env`。

## 自动连接行为

自动连接默认面向已登录系统的当前用户，不要求把校园网密码或 Windows 登录密码写进任务定义。

| 平台 | 后台机制 | 触发方式 |
| --- | --- | --- |
| Windows | 用户任务计划 `CampusLink` | 用户登录后约 10 秒、网络连接事件后约 10 秒、每 5 分钟补偿检查 |
| macOS | 用户 LaunchAgent | 加载或用户登录时执行，每 5 分钟检查 |
| Linux | systemd 用户 service + timer | 用户管理器启动后约 10 秒、每次执行结束后 5 分钟 |

Windows 任务允许电池供电，不唤醒电脑，使用当前用户交互会话并忽略重叠触发。网络事件是否触发取决于系统日志，定时检查负责补偿。macOS 和 Linux 首期以定时补偿覆盖网络切换，不承诺切换后立即执行。

Linux 启用前会检查 systemd 用户会话；不支持时明确报错，仍可手动运行。尚未登录系统就进行认证不属于默认安装模式。

所有登录任务使用同一配置目录中的系统文件锁，防止同时认证；进程退出时锁自动释放。已在线时直接成功，不再提交密码。网络错误在下一次调度时重试；网关明确拒绝认证（包含验证码、强制改密等）时暂停自动提交。解决问题后可手动成功登录，或运行 `autostart enable` 恢复。

运行记录保存为 `activity.log`，只记录概括状态和 DNS 提示，不记录密码或网关原始认证响应。达到 256 KiB 后保留一份轮转备份。`state.json` 记录最近尝试、成功时间和暂停原因。

更新程序后，使用新下载的程序运行 `autostart enable`，即可替换后台使用的程序副本和任务定义。该命令也会清除暂停状态。

## 认证与 DNS

程序通过 `/api/r/default` 发现客户端 IP、NAS ID 等参数，获取 CSRF token 和 Cookie，检查在线状态，然后用表单 POST 依次调用 `/api/account/check` 和 `/api/account/login`。

默认通过系统 DNS 解析 `nap.cug.edu.cn`，使用 HTTPS 并校验证书：

- 解析结果包含 `192.168.167.72`：正常继续。
- 不包含该 IP：提示实际解析地址和排查建议，仍通过域名连接。
- DNS 查询失败或没有返回地址：报错，不回退到固定 IP。

该 IP 仅用于提醒。VPN/TUN 或 Fake-IP DNS 可能影响解析结果，需要结合实际网络检查。默认校园网域名不使用环境变量 HTTP 代理；自定义网关按其地址连接。密码不会放进登录请求 URL，但显式选择 HTTP 自定义网关将不具备 HTTPS 传输保护。

## 故障排查

先运行：

```bash
campuslink doctor
campuslink logs
```

- 未保存配置：运行 `campuslink setup`。
- 凭据服务不可访问：解锁系统凭据服务，确认任务运行在配置时的同一用户下；无桌面环境可显式选择文件存储。
- DNS 或 HTTPS 错误：检查校园网连接、系统 DNS、VPN/TUN 和证书，确认浏览器能打开 `https://nap.cug.edu.cn`。
- 自动连接暂停：在浏览器处理密码错误、验证码或强制改密，再运行 `setup`、成功的 `login` 或 `autostart enable`。
- IP / NAS ID 自动发现失败：从浏览器认证页的重定向地址读取 `ip` / `nasId`，通过 `setup --ip ... --nas-id ...` 保存。两个值均指定时跳过重定向发现；更换网络后可能需要清空或更新。
- 后台程序未更新：用新版本执行 `autostart enable`。

## 开发与验证

```bash
go test ./...
go vet ./...
go test -race ./...
```

测试覆盖认证接口、DNS 提示、凭据覆盖、保存与读取、文件锁、日志轮转、只读诊断、暂停与恢复，以及三个平台的任务生成与安装卸载流程。任务生命周期测试使用模拟命令，不会安装真实后台任务或修改测试机器的凭据。

GitHub Actions 在 Windows、macOS 和 Linux 上运行测试，并交叉编译六种系统/架构组合。发版工作流由 `v*` tag 或手动指定 tag 触发。

## 许可证

CampusLink 使用 [MIT License](LICENSE)。
