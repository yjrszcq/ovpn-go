# 部署更新政策

本文适用于 ovpn-go Docker 镜像和自包含 Linux 压缩包。裸机命令及服务管理方法见 [Linux 裸机指南](v4/native-linux.md)。

## 版本模型

项目版本、OpenVPN runtime 和持久化数据 schema 是独立版本轴：

- `IMAGE_VERSION`：Docker 与裸机共用的项目发布版本。
- `DATA_SCHEMA`：持久化结构化状态格式。
- `OPENVPN_VERSION`：内置 OpenVPN Community Edition 版本。
- `GO_RUNTIME_VERSION`：Go 二进制报告的应用版本，由 `IMAGE_VERSION` 派生，不是 Go SDK 版本。

`ovpn version --json` 报告项目版本、schema、构建来源及 Go SDK 版本；`ovpn runtime capabilities --json` 报告 OpenVPN 兼容性契约。候选版本范围只限制自动化，不是兼容性承诺。

## 交付边界

项目程序、模板、迁移支持和兼容性契约随 Docker 镜像或裸机压缩包一起交付，裸机压缩包还包含独立用户态运行时及依赖。运行中的 VPN 服务不会自行下载或更新代码；单独执行的裸机安装器和更新器会下载并验证已发布 Release 附件。

更新不能仅因服务启动就静默应用已改动的 YAML、重写凭据或迁移 schema。已初始化实例的显式 `server run` 使用已应用的 SQLite 配置；应用期望 YAML 仍需由操作员明确执行。

## 选择发布版本

- GHCR：`ghcr.io/yjrszcq/ovpn-go`；数字 tag `X.Y.Z`、`X.Y` 和 `X` 对应项目版本，也提供 `latest` 和已验证的 `ovpn<version>` 别名。
- Docker Hub：`szcq/openvpn`；数字 tag 对应内置 OpenVPN 版本，`latest` 跟随稳定镜像。
- 裸机压缩包：`openvpn-server-X.Y.Z-linux-amd64.tar.gz` 和 `openvpn-server-X.Y.Z-linux-arm64.tar.gz`，附带 `SHA256SUMS`；按宿主架构选择并验证校验和。

生产环境固定明确版本。仓库更名后，GHCR 引用应改为当前仓库命名空间，Docker Hub 名称不变。两个架构压缩包和 `SHA256SUMS` 齐全后，裸机 Release 才可用于安装。

## 同 schema 更新

共用 data schema 的版本以相同方式解释数据。更新前备份数据和配置，同 schema 变更不要执行 `migrate apply`。

Docker 部署先停止服务，为在线和 maintenance 服务选择相同新镜像并拉取，执行只读 `state doctor` 后重建在线服务。保留原有数据卷，检查版本、状态及运行健康，详见[操作手册](v4/operations.md#不变-schema-的镜像更新)。

安装器管理的裸机部署使用[在线更新器](v4/native-linux.md#更新与恢复)。更新器保留原先运行/停止状态和开机启动设置，停服后保存快照，重启后检查健康；替换或健康检查失败时恢复旧程序和快照。更新中断后再次执行更新器恢复事务。自定义目录需传入相同的 `OVPN_INSTALL_DIR`，托管恢复快照覆盖安装目录内的 `config/`、`data/` 和 `run/`。手工启动的实例需先自行停止。

裸机更新器只接受兼容 schema 并拒绝降级，不负责 Docker 到裸机的部署转换。

## Schema 变化与回滚

更新 schema 需要在停服后执行明确支持的迁移，见[数据 schema 升级政策](data-schema-upgrade-policy.md)；裸机在线更新器不执行跨 schema 迁移。

同 schema 的 Docker 回滚可使用旧镜像重建服务。裸机更新成功后，在线更新器不能用于降级；主动回滚需要停服，恢复匹配的旧程序及完整备份。更新失败时自动回滚与操作员主动降级是不同流程。

schema 迁移后，只切换镜像或可执行文件并不足够，必须恢复完整迁移前快照，并使用支持该 schema 的程序。不要只恢复 SQLite 文件，也不要将备份合并到 live 状态中。

## 发布验证

发布验证覆盖初始化、PKI、客户端与地址管理、配置、备份恢复、迁移兼容性和真实 VPN 流量。裸机附件仅在两种原生架构的构建与验收作业通过后上传。实际验证的发行版和硬件/仿真边界以各 Release 的兼容性与验证说明为准。
