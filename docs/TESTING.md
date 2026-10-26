# 测试与 CI

## 目标与边界

公开仓库的测试验证通用逻辑、迁移、事务边界和部署脚本，只使用合成数据。真实服务器、套餐、出口 IP、订阅地址、用户账号、代理配置和凭据的验收属于私有流程，不进入本仓库。

测试的首要目标是防止回归，而不是追求覆盖率数字。新增持久模型、迁移、事务、认证、越权、凭据隐藏、订阅撤销、主从下发或部署事务时，必须同时提交可复现的回归测试。

## 测试层次

| 层 | 位置 | 本地命令 | 说明 |
| --- | --- | --- | --- |
| Go 单元/回归 | `cmd/sbmgr/*_test.go`、`internal/mesh/*_test.go` | `go test ./...` | 状态模型、SQLite 迁移与增量写入、配置渲染、配额、主从、订阅、Web 边界 |
| Go 并发 | 同上 | `go test -race ./...` | 跨进程锁、IPC、任务回执、worker 生命周期 |
| 前端单元 | `frontend/src/**/*.test.ts` | `npm --prefix frontend test` | 格式化、下载文件名、线路名称与选择逻辑 |
| 前端类型与构建 | `frontend/` | `npm --prefix frontend run typecheck`、`npm --prefix frontend run build` | TypeScript 与内嵌资源 |
| 部署脚本 | `deploy/test-deploy-scripts.sh` | `sh deploy/test-deploy-scripts.sh` | 路径解析、systemd 模板、状态文件选择；仅 Linux |
| 公开树 | `scripts/test_check_public_tree.py`、`scripts/check_public_tree.py` | `python3 scripts/test_check_public_tree.py` 等 | 文件名、凭据格式、私有路径、文档网段 |
| 特权隔离 | `cmd/sbmgr/*_linux_test.go` | CI 中以 root + systemd 沙箱运行 | Web/订阅 worker 降权、TLS、父进程退出与崩溃恢复 |
| 私有验收 | 仓库外 | 由部署方执行 | 真实 sing-box、nftables、HTTPS、订阅与代理请求 |

## 本地验证

```sh
gofmt -w cmd/sbmgr internal/mesh
go vet ./...
go test ./...
go test -race ./...
npm --prefix frontend ci
npm --prefix frontend run format:check
npm --prefix frontend run typecheck
npm --prefix frontend test
npm --prefix frontend run build
python3 scripts/test_check_public_tree.py
python3 scripts/check_public_tree.py
for script in deploy/*.sh scripts/*.sh; do sh -n "$script"; done
sh deploy/test-deploy-scripts.sh
git diff --check
```

一键入口：`sh scripts/verify.sh`；Windows：`./scripts/verify.ps1 -LinuxBuild`。`make check` 覆盖常用门禁，`make release-check` 额外运行 race。

Linux 专属测试在 macOS/Windows 上会明确跳过或由 build tag 排除，不把“本机跳过”当成通过；涉及 systemd、nftables、降权或证书时，以 Linux CI 或私有目标环境为准。

## CI 门禁

`.github/workflows/ci.yml` 按推送、PR 和手动触发运行：

| Job | 内容 |
| --- | --- |
| `policy` | 公开树检查、检查器自测、Shell 语法、部署脚本测试 |
| `frontend` | `npm ci`、格式、类型、单元测试、构建，并上传内嵌资源 |
| `go-test` | 模块整洁、`gofmt`、`go vet`、`go test -race -shuffle -cover`、`govulncheck`、空白检查 |
| `privilege` | 以 root 和受限 systemd 沙箱运行 Web/订阅降权与生命周期测试 |
| `go-test-macos` | macOS 全量 Go 测试 |
| `go-compile-windows` | Windows `go vet` 与测试编译（不运行平台相关用例） |
| `build` | 下载前端产物，构建 `CGO_ENABLED=0` Linux AMD64 二进制并执行 `version` 冒烟检查 |

CodeQL 在推送、PR 和每周计划中扫描 Go；Dependabot 每周更新 Go、npm 与 Actions。PR 的旧运行会被新提交取消，分支和标签运行保留完整结果。

标签发布由 `.github/workflows/release.yml` 执行，详见[发布流程](RELEASING.md)。

## 测试数据规则

- 文档网段：`192.0.2.0/24`、`198.51.100.0/24`、`203.0.113.0/24`；域名使用 `example.com`、`example.net` 等保留名。
- UUID、token、密钥和密码必须是明显的伪值；测试生成的临时密钥只存在于 `t.TempDir()`。
- 测试写入临时目录，不读取真实 `state.db`、配置、日志、备份或订阅；需要基础配置时在临时目录构造最小模板。
- 失败输出只打印结构体或断言，不回显真实凭据；公开 issue/PR 同样遵守。

## 覆盖率

当前基线约 52%（`make coverage`）。覆盖率用于发现空白区域，不设硬性阈值，也不以删除断言或跳过测试换取绿灯。新增核心逻辑应带来相应测试；修复缺陷时优先先写能复现问题的测试。

## 调试

```sh
go test ./cmd/sbmgr -run TestName -v
go test ./cmd/sbmgr -run TestName -count=1 -shuffle=on
go test -race ./cmd/sbmgr -run TestName -v
npm --prefix frontend test -- src/format.test.ts
```

特权隔离测试默认跳过，由 CI 的 `privilege` job 以 root 和受限 systemd 沙箱运行。本地复现需要 root-owned 目录和显式 `SBMGR_RUN_PRIVILEGE_TEST=1`，步骤参考 [.github/workflows/ci.yml](../.github/workflows/ci.yml)；不要指向真实生产主机。
