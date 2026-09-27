# 开发验证

先遵守 [AGENTS.md](../AGENTS.md)。Go 工具链以 [go.mod](../go.mod) 为准；Linux 运行依赖见 [README](../README.md)。公开仓库只保存通用源码、构建文件和使用文档，不保存生产状态、真实代理配置、凭据或部署方验收资料。

## macOS 开发环境

从其他系统迁移源码后，需在 Mac 上单独安装工具链。使用 Homebrew 时运行 `brew install go`，再用 `go version` 确认；项目所需版本仍以 `go.mod` 为准，不复制 Windows 的 Go 二进制或 `node_modules`。前端依赖通过 `npm --prefix frontend ci` 重新安装。

## 常规验证

```sh
gofmt -w cmd/sbmgr internal/mesh
go vet ./...
go test ./...                         # 编译所有 Go 包
npm --prefix frontend ci
npm --prefix frontend run format:check
npm --prefix frontend run build
python3 scripts/check_public_tree.py
git diff --check
```

一键入口：`sh scripts/verify.sh`（含脚本语法和 Linux 构建）；Windows 为 `./scripts/verify.ps1 -LinuxBuild`。Linux 生产构建须 `CGO_ENABLED=0`，否则 Web/订阅进程无法保证全线程降权。

| 改动 | 重点验证 |
| --- | --- |
| 持久模型 / schema | 版本迁移、坏输入拒绝、失败不残留；使用临时本地状态，不提交状态文件 |
| 用户分组 | 旧用户迁入默认组不变更策略；独立配额、个人覆盖、恢复继承、批量失败全回滚、并发编辑及普通用户隔离 |
| 自助设备 | 0/1–100 名额与停用设备计数、降低名额不删设备、仅复制本人授权设备、身份新建、订阅重置旧地址立即 404、节点连接身份更换、旧配置待各入口应用后失效；认证/来源/CSRF/归属/并发与事务失败 |
| 机器流量 | Linux 物理网卡计数器、主从上报与持久增量、跨重启/重置基线、循环日期边界、月末和闰年锚点、旧规则迁移、历史分页、无数据和部分覆盖、与用户配额隔离；验证不使用真实服务器数据 |
| 配置 / 限速 | 非托管身份保留、候选校验、应用失败恢复；Linux 构建 |
| 主从 / RPC | 兼容性、角色复用、循环拒绝、幂等和恢复决定 |
| Web | 登录来源、CSRF、会话撤销、凭据隐藏、动作白名单和事务边界 |
| 自助应用 | 新增/删除设备后的后台应用成功与失败恢复、管理员待应用状态下拒绝变更；用临时配置验证 |
| 订阅 | 即时撤销、来源限流、IPC 边界、权限和父子进程生命周期 |
| 部署 / 证书 | Shell 语法、Linux 构建、来源和权限检查 |

真实服务器、套餐、出口 IP、订阅地址、用户账号和凭据不属于公开仓库验证范围；部署验收须在目标环境的私有流程中完成。

## 构建与提交

开发 Web 构建：先执行 `npm --prefix frontend ci` 与 `npm --prefix frontend run build`，再运行 `go build -o sbmgr ./cmd/sbmgr`。Node.js 22.12+ 仅在开发/发布构建时需要；发布 CI 使用 Node.js 24。纯 Go 构建不安装前端依赖时，程序会明确提示缺少内嵌管理页面。正式构建：`./deploy/build-linux.sh`，要求干净工作树且 HEAD 精确对应 `vX.Y.Z` tag，注入版本及 commit；不手写版本历史。部署与来源核验见[运维指南](OPERATIONS.md)。

提交前审阅 `git status --short` 和 `git diff --cached`；不得添加数据库、运行 YAML/JSON、证书、私钥、日志、备份、订阅或任何真实部署资料。`scripts/check_public_tree.py` 会检查候选文件名、凭据格式、私有路径和非文档公网地址。
