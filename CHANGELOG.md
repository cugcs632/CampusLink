# v1.0.0

CampusLink 1.0 提供统一的 Windows、macOS 和 Linux 配置、认证及自动连接体验，并适配当前校园网认证接口。

## 新增

- `setup` 配置向导：隐藏输入密码、验证网关连接、保存配置并选择自动连接。
- `login`、`status`、`doctor`、`logs` 和 `autostart enable|disable` 命令。
- Windows 凭据管理器、macOS 钥匙串和 Linux Secret Service 存储；可明确选择受文件权限保护的明文存储。
- 三平台原生用户级后台任务，使用稳定目录中的程序副本；支持重新安装和更新。
- 并发锁、后台认证拒绝后的暂停与恢复、日志轮转和运行状态记录。
- Windows、macOS、Linux 的 amd64 / arm64 六种二进制产物。

## 认证与连接

- 使用新版 CSRF、在线状态、账号检查和表单登录接口。
- 默认通过系统 DNS 解析 `nap.cug.edu.cn`，保留 HTTPS 证书校验。
- DNS 结果不包含 `192.168.167.72` 时提醒用户，仍通过域名连接；不强制替换连接 IP。
- `status` 和 `doctor` 使用只读认证接口，不提交密码。

## 升级说明

- 此版本移除旧 SRun 认证协议与专用参数。环境变量使用 `CAMPUSLINK_*`，不再读取 `SRUN_*`。
- 下载并解压对应产物，Windows 执行 `.\campuslink.exe setup`，macOS/Linux 执行 `./campuslink setup`。
- 先停用此前手动创建的自动认证任务，再通过向导启用新的后台任务，避免重复运行。
- 后续更新可使用新下载的程序执行 `autostart enable`，替换后台使用的程序副本。
- 自动连接默认服务于已登录系统的当前用户。Linux 需要 systemd 用户会话；系统凭据服务不可用时可显式使用 `setup --storage file`。
- 若设备已经在线，`setup` 的连接检查不能验证新输入密码是否正确，需在离线认证时确认。

## 验证

- 单元测试、静态检查和竞态检查；CI 在 Windows、macOS 和 Linux 上运行。
- 后台任务测试覆盖任务定义和模拟安装卸载，不会安装真实后台任务或修改测试机器的系统凭据。
