# 开发交接与易踩坑

执行新任务前先阅读本文件。详细复现、构建和验证证据放在 `.checkpoint-karpathy/private/records/`，用户文档只写用户需要的操作和兼容性说明。

## CLI 参数与恢复配置

- `config export -r/--restore` 恢复当前 YAML；`config restore` 已移除。`--restore` 与 `--output` 互斥，`--yes` 仅用于恢复模式。
- 短参数按命令解释。全局 `canonicalOption` 中的 `-r` 仍对应日志命令的 `--raw`；配置导出须在自己的解析器中处理 `-r`，不能直接修改全局映射。
- 恢复只改 YAML，不改 applied 数据、不重启服务；初始化前生成默认配置。验证时覆盖无数据库、损坏状态、缺少确认、符号链接和普通导出不覆盖文件。

## 容器构建与验证

- Docker 运行入口是 `/usr/local/bin/ovpn`。`/usr/local/lib/openvpn-container/go/` 是构建时的中间目录，仅替换其中的文件不会更新实际入口。运行测试前核对镜像的版本和新命令帮助，防止测到旧程序。
- Docker COPY 到符号链接路径时可能覆盖链接目标。例如 Alpine 的 `/usr/bin/flock` 可能指向 BusyBox；直接覆盖可能同时破坏 `/bin/sh`。必须先确认链接，并按当前删除约定单独移除明确的链接文件，再复制普通文件。
- 本机已缓存的 Go 镜像标签可能只对应某一个架构；不要把本地存在标签等同于所需架构可用。镜像拉取或依赖下载失败时保留原始日志；复用已验证依赖的本地运行测试不能冒充完整发布构建通过。
- 本次配置导出调整的验证证据见 `.checkpoint-karpathy/private/records/config-export-restore-20261026/`。测试目录按当前约定保留，未进行批量文件删除。
