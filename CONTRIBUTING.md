# 贡献指南

感谢参与 sbmgr。先读 [AGENTS.md](AGENTS.md)、[架构与代码入口](docs/ARCHITECTURE.md) 与[开发验证](docs/DEVELOPMENT.md)；测试分层和 CI 门禁见[测试与 CI](docs/TESTING.md)。

## 开始之前

- 运行环境：Go 版本以 [go.mod](go.mod) 为准；前端需要 Node.js 22.12+（CI 使用 24）；Linux 部署集成测试需要 systemd、GNU `stat`、`getent` 和 `realpath`。
- 先搜索现有 issue / PR，避免重复；行为变化、兼容性影响和失败恢复方式写在 PR 描述中。
- 每次变更保持单一目的。重构与行为修改尽量分开，便于审查和回滚。

## 本地开发

```sh
npm --prefix frontend ci
sh scripts/verify.sh            # macOS/Linux；Windows 用 ./scripts/verify.ps1 -LinuxBuild
```

常用目标：`go test ./...`、`go test -race ./...`、`npm --prefix frontend test`、`make check`、`make coverage`。改 Go 代码后运行 `gofmt -w cmd/sbmgr internal/mesh`。

## 测试要求

- 新增或修改持久模型、schema 迁移、事务、配额/计费、认证、越权、凭据隐藏、订阅撤销、主从下发或部署事务时，必须带回归测试。
- 测试只使用文档网段（`192.0.2.0/24`、`198.51.100.0/24`、`203.0.113.0/24`）、`example.com` 类保留域名、伪 UUID/token 和临时目录。
- 不得把真实服务器、套餐、出口 IP、订阅地址、用户账号、代理配置、数据库、证书、日志或备份写进测试、快照或 issue。
- 前端交互至少覆盖纯逻辑单元测试；认证、CSRF、越权、凭据隐藏、事务失败和响应边界优先自动验证，页面另做浏览器检查。
- Linux 专属行为（systemd、nftables、降权、证书）由 Linux CI 或私有的目标环境验收覆盖；不要在公开测试中伪造生产状态。

## 提交与 PR

- 从 `main` 建分支，提交信息使用 Conventional Commits 前缀：`feat`、`fix`、`docs`、`test`、`ci`、`refactor`、`perf`、`chore`、`build`。示例：`test(state): cover rehash repair after external edits`。
- PR 说明问题、触发条件、修改后的行为、兼容与恢复方式，以及实际运行的检查；未验证范围要写明。
- 提交前审阅 `git status --short` 与 `git diff --cached`。`scripts/check_public_tree.py` 会拒绝候选文件名、凭据格式、私有路径和非文档公网地址。
- 所有必选 CI 检查通过后再合并；不要通过删除断言、跳过测试或降低覆盖来让流水线变绿。

## 发布

版本由维护者在 `main` 上打 `vX.Y.Z` 标签触发 Release 工作流；流程、产物与校验见[发布流程](docs/RELEASING.md)。贡献者不需要手动修改版本号或维护二进制。

## 行为与许可

参与讨论和贡献时遵守[行为准则](CODE_OF_CONDUCT.md)。贡献按 [GPL-3.0-only](LICENSE) 授权，贡献者保留版权并保证有权提供内容。第三方内容须注明来源、保留版权和许可证，确认许可兼容。
