# Linux 裸机安装

Linux 压缩包包含 `ovpn`、OpenVPN、OpenSSL、Easy-RSA、musl 加载器、共享库、providers、网络工具、Shell 和编辑器，运行时无需在宿主安装这些组件。现有 Docker 部署方式、配置和命令继续保留。

x86-64 使用 `linux-amd64`，AArch64 使用 `linux-arm64`。宿主需要 Linux TUN 支持、网络管理权限，以及可供客户端访问的 OpenVPN 端口。请自行配置宿主及云防火墙；`ovpn` 只维护实例规则，不关闭 SELinux 或 firewalld。IPv4 隧道支持通过 IPv4/IPv6 使用 UDP/TCP 传输，不支持 IPv6 隧道地址。

兼容范围包括 Debian 13.7、Ubuntu 26.10、Rocky Linux 10.2、Alpine 3.24.3 和当前 Arch Linux。AArch64 上的 Arch 兼容性单独指 Arch Linux ARM。本地 AArch64 验证使用仿真；Release 工作流还要求原生 AArch64 验收通过后才上传附件。

## 在线安装

请等待 Release 页面上的两个架构压缩包和 `SHA256SUMS` 齐全后再安装。引导脚本需要 Bash、curl、tar、`sha256sum` 及常见 Linux 命令行工具，不会安装宿主软件包。

```bash
sudo bash -c "$(curl -fsSL 'https://raw.githubusercontent.com/yjrszcq/ovpn-go/main/install.sh')"
```

安装器先询问语言（默认 English），再询问安装、更新或卸载（默认安装）。安装目录默认 `/opt/ovpn`，版本默认最新稳定 Release；其他有默认值的选项直接回车即可。公网域名或 IP 必须填写。后续交互包括 VPN 网段、传输协议、端口、NAT、DNS 和可选高级配置。

脚本自动识别宿主架构，验证压缩包，创建 `/usr/local/bin/ovpn` 并注册 systemd 或 OpenRC 服务。配置经校验和确认后一次性写入，再询问是否启用开机启动、是否立即启动（均默认 yes）。遇到无关的已有命令、目录或服务定义时拒绝覆盖，每台宿主支持一个托管服务。

按 Ctrl-C 可中断；重新运行并选择相同操作后，保留已完成的答案，从未完成的问题继续。未完成的交互不会生成生效配置；已有配置不会被重新安装覆盖。安装或更新参数均在交互中选择，无需设置环境变量。请确认网段不重叠，并自行在宿主与云防火墙放行端口。

## 配置与启动

交互安装已完成配置和启动选择。以下命令用于后续手动编辑、启动，或选择暂不启动后的操作。

```bash
sudo ovpn config edit
```

将 `server.endpoint` 改为公网域名或 IP，并选择不与现有网络重叠的 `ipv4.network`。启动前检查传输方式、NAT、路由和 DNS 设置。也可使用包内编辑器 `/opt/ovpn/bin/nano`。

systemd：

```bash
sudo systemctl enable --now ovpn
sudo systemctl status ovpn
```

OpenRC：

```bash
sudo rc-update add ovpn default
sudo rc-service ovpn start
```

前台运行：

```bash
sudo /opt/ovpn/ovpn server run
```

无参数 `ovpn` 显示命令树，不写入初始化状态。`server run` 在当前终端前台运行；数据为空时使用已有配置初始化，首次缺少配置时提示操作方法并退出。同一实例只能运行一份，前台和托管启动共用实例锁。SIGTERM 或 Ctrl-C 会停止子进程并清理实例网络规则。前台进程停止后重新运行即可重启；托管服务使用对应的 stop/restart 命令。

### 首次初始化与配置应用

`server init` 会创建 PKI、数据库和派生文件，并将初始 YAML 保存为 applied revision 1，但不启动服务，也不需要额外 apply。通常无需手动 init，首次 `server run` 或系统服务启动会自动完成。

| 场景 | 操作 |
| --- | --- |
| 安装后立即启动 | 自动初始化并启动，无需 init/apply |
| 安装后暂不启动，尚未初始化 | 编辑配置后直接启动，无需 init/apply |
| 已手动 init，随后编辑 YAML，即使还没运行过服务 | 使用 `sudo ovpn config edit --apply` 或 `config plan` / `config apply` |
| 已有实例停止期间编辑 YAML | 应用变更后再启动；仅重启仍使用原 applied 快照 |

安装时选择暂不启动，只保存配置和注册服务。判断是否需要 apply 的依据是是否已初始化。首次初始化后，启动不会隐式应用 YAML 变化；缺失或无效的 YAML 会给出警告，已有实例继续使用 applied 快照。数据库损坏或状态不完整会报错，不会重新初始化覆盖数据。

## 文件与日常命令

| 相对解压目录的路径 | 用途 |
| --- | --- |
| `ovpn` | 可重定位的 CLI 与前台启动入口 |
| `runtime/`、`bin/` | 包内程序、库、资源和许可证 |
| `config/config.yaml` | 用户配置 |
| `data/` | SQLite、PKI、客户端配置、派生配置和审计状态 |
| `logs/` | 指向 `data/logs/`，包含轮转后的 OpenVPN 日志 |
| `run/` | 运行时套接字和服务诊断输出 |

```bash
sudo ovpn runtime health
sudo ovpn client create laptop --ipv4 auto
sudo ovpn client export laptop --output /root/laptop.ovpn
sudo ovpn state doctor
sudo ovpn config plan
sudo ovpn config apply --yes
```

CLI、可选 REST API 和已有 `OVPN_*` 覆盖继续有效。覆盖按进程生效，服务与 CLI 应使用相同设置。使用安装器管理更新时，请将可写状态保留在安装目录内的 `config/`、`data/` 和 `run/` 中，确保恢复快照包含这些状态。启动诊断可通过 `journalctl -u ovpn` 查看；OpenRC 将其写入 `run/service*.log`。API 的启用和保护方式见 [REST API 指南](rest-api.md)。

## API 与离线维护

API 默认关闭。首次初始化前直接编辑 YAML；以下 apply 操作用于已初始化的实例。安装向导可以配置 API；之后运行 `sudo ovpn config edit -a`，修改以下 YAML 并确认应用：

```yaml
api:
  enabled: true
  listenIP: 127.0.0.1
  port: 11940
  cors: []
```

空 `cors` 列表关闭 CORS。远程访问请使用 HTTPS 反向代理；参见 [REST API 指南](rest-api.md)。

为此实例创建 API key：

```bash
sudo /opt/ovpn/ovpn api key create frontend-production --output /root/frontend.key
```

离线修复、配置或迁移操作前先停止服务。需要 maintenance 授权的命令采用以下形式：

```bash
sudo env OVPN_MAINTENANCE=true /opt/ovpn/ovpn migrate plan
```

`migrate plan` 只读；同 schema 更新不要执行 `migrate apply`。支持的迁移来源和确认要求见[命令参考](commands.md)。

## 更新与恢复

```bash
sudo bash -c "$(curl -fsSL 'https://raw.githubusercontent.com/yjrszcq/ovpn-go/main/install.sh')"
```

重新运行脚本并选择“更新”。更新器先下载验证，再锁定安装目录。服务原先运行时，会停止、保存快照、更新并重启检查健康；原先停止则继续停止。用户配置、证书、数据库、客户端配置、日志、权限和开机启动状态会保留。替换或健康检查失败时恢复旧程序、原服务定义和数据快照。更新中断后，再次执行更新器即可恢复事务；若 `.update-backup/` 仍保留，表示恢复需要处理，请在实例恢复健康前保留它。

更新同时迁移本实例的服务入口至 `ovpn server run`；原服务定义与程序、数据一起纳入恢复快照。同版本更新和重新安装也会修复旧入口。更新前运行的服务恢复运行，停止的服务保持停止，开机启动状态不变。

在交互中确认安装目录，选择最新稳定版或填写已发布版本号。

更新仅允许兼容的数据 schema，并拒绝降级。重复安装、同版本更新不会破坏状态。手工启动的实例需要先自行停止，更新器不会自动终止它们。独立备份方法见[操作手册](operations.md)。

## 卸载

运行相同安装命令，选择“卸载”。默认保留配置、证书、数据库和日志，停止服务后移除程序、服务定义及命令链接。以后重新安装到相同目录可继续使用这些数据。永久删除数据需选择不保留，输入 `DELETE` 并单独确认卸载。请事先备份；删除后无法通过安装器恢复。

## 离线备份与恢复

以下示例使用默认安装目录和 systemd。OpenRC 将 `systemctl stop/start ovpn` 换为 `rc-service ovpn stop/start`；前台运行则停止进程，操作后手工重启。复制数据前需停止全部 writer，包括维护命令。

```bash
sudo systemctl stop ovpn
sudo /opt/ovpn/bin/tar -czf /root/ovpn-backup.tar.gz -C /opt/ovpn config data
sudo chmod 600 /root/ovpn-backup.tar.gz
sudo systemctl start ovpn
```

备份包含 SQLite、CA/服务端/客户端私钥、证书和 profile，应加密保存；数据目录和对应配置必须一起备份和恢复。

恢复时保持服务停止，将可信备份解压到新目录，完整替换数据和配置目录：

```bash
sudo systemctl stop ovpn
sudo mkdir -m 700 /opt/ovpn/restore-work
sudo /opt/ovpn/bin/tar -xzf /root/ovpn-backup.tar.gz -C /opt/ovpn/restore-work
sudo mv /opt/ovpn/data /opt/ovpn/data-before-restore
sudo mv /opt/ovpn/config /opt/ovpn/config-before-restore
sudo mv /opt/ovpn/restore-work/data /opt/ovpn/data
sudo mv /opt/ovpn/restore-work/config /opt/ovpn/config
sudo /opt/ovpn/ovpn state doctor
sudo systemctl start ovpn
sudo /opt/ovpn/ovpn runtime health
```

使用支持该备份 schema 的程序。不要只恢复 `state.db`，也不要合并到已有数据中；诊断和真实客户端连接验证完成前，保留原目录。如果恢复后的解压路径与备份原安装位置不同，在启动前执行 `sudo /opt/ovpn/ovpn repair apply --yes`，重建包含绝对路径的派生文件。

## 手工解压与移动目录

从同一 Release 下载匹配架构的压缩包和 `SHA256SUMS`，按需替换 `X.Y.Z` 和架构：

```bash
awk '$2 == "openvpn-server-X.Y.Z-linux-amd64.tar.gz" {print}' SHA256SUMS | sha256sum -c -
tar -xzf openvpn-server-X.Y.Z-linux-amd64.tar.gz
cd openvpn-server
cp config/config.example.yaml config/config.yaml
# 启动前编辑 config/config.yaml。
sudo ./ovpn server run
```

手工前台运行不需要服务管理器。需要通过 PATH 使用 CLI 时，添加解压根目录，例如 `export PATH="/opt/ovpn:$PATH"`，并保持整个目录完整。支持绝对路径、符号链接、不同工作目录和含空格的路径。`sudo` 可能重置 PATH，此时请使用程序绝对路径或安装器创建的 `/usr/local/bin/ovpn` 链接。

初始化前可直接移动目录。已初始化的实例需要先停止，再移动完整目录，然后执行 `sudo /new/path/ovpn repair apply --yes`，重建含绝对路径的派生文件后再启动。仍指向旧位置的服务定义和命令链接也需要更新。安装器管理的更新操作使用原先注册的目录和服务；手工移动后需要先同步调整这些路径。

## 手工注册系统服务

手工解压用户可使用包内 `runtime/share/services/` 的示例（仓库中为 `examples/services/`）。示例假定安装目录为 `/opt/ovpn`；使用其他目录时，先修改示例中所有绝对路径，再复制。先复制并编辑 `config/config.example.yaml` 为 `config/config.yaml`。不要与同一实例的前台进程同时运行；已由安装器注册的服务无需重复注册。

### systemd

```bash
sudo cp /opt/ovpn/runtime/share/services/systemd/ovpn.service /etc/systemd/system/ovpn.service
sudo systemctl daemon-reload
sudo systemctl start ovpn
sudo systemctl status ovpn
sudo systemctl restart ovpn
sudo systemctl stop ovpn
sudo systemctl enable ovpn
sudo systemctl disable ovpn
sudo journalctl -u ovpn -n 50
```

停止并注销服务，保留程序和所有实例数据：

```bash
sudo systemctl stop ovpn
sudo systemctl disable ovpn
sudo rm /etc/systemd/system/ovpn.service
sudo systemctl daemon-reload
```

### OpenRC

```bash
sudo cp /opt/ovpn/runtime/share/services/openrc/ovpn /etc/init.d/ovpn
sudo chmod 755 /etc/init.d/ovpn
sudo rc-service ovpn start
sudo rc-service ovpn status
sudo rc-service ovpn restart
sudo rc-service ovpn stop
sudo rc-update add ovpn default
sudo rc-update del ovpn default
sudo tail -n 50 /opt/ovpn/run/service.log /opt/ovpn/run/service-error.log
```

停止并注销服务，保留程序和所有实例数据：

```bash
sudo rc-service ovpn stop
sudo rc-update del ovpn default
sudo rm /etc/init.d/ovpn
```

两套服务均明确执行 `ovpn server run`。后台托管由服务管理器完成，不向程序添加 daemon 参数。OpenVPN 自身日志位于 `logs/openvpn.log`。

## 安装试运行

在仓库目录运行 `bash install.sh --dry-run`，无需 sudo。保留语言、操作和配置交互，展示草稿及将执行的操作，不下载、不写入安装目录、配置或续填记录，也不操作服务。试运行不验证发布附件或运行健康；退出后不会保存答案。更新、卸载预览同样从操作菜单选择。
