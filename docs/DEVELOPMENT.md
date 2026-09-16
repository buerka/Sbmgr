# 开发验证

先遵守 [AGENTS.md](../AGENTS.md)。Go 工具链以 [go.mod](../go.mod) 为准；Linux 运行依赖见 [README](../README.md)。

## 常规验证

```sh
gofmt -w cmd/sbmgr internal/mesh
go vet ./...
go test ./...
python3 scripts/test_check_public_tree.py
python3 scripts/check_public_tree.py
git diff --check
```

一键入口：`sh scripts/verify.sh`（含脚本语法、Linux 构建）；Windows 为 `./scripts/verify.ps1 -LinuxBuild`，脚本语法另在 Git Bash/Linux 检查。Linux 生产构建须 `CGO_ENABLED=0`，否则订阅进程无法保证全线程降权。

| 改动 | 重点验证 |
| --- | --- |
| 持久模型 / schema | 分别递增版本；旧 JSON/SQLite 迁移、幂等、坏输入无残留、新状态校验 |
| 配置 / 限速 | 非托管身份保留、候选校验、应用失败与恢复；Linux 构建 |
| 主从 / RPC | 兼容性校验、混合协议、角色复用、拒绝循环、幂等、丢失响应与恢复决定 |
| CUI | 菜单发现、快捷键、中文宽度、窄/短终端、输入编辑 |
| 订阅 | 即时撤销、来源限流、IPC 边界、权限和父子进程生命周期 |
| 部署 / 证书 | Shell 语法、`sh deploy/test-deploy-scripts.sh`、Linux 构建 |

测试默认使用临时目录、虚构数据与模拟传输。部署脚本自检使用桩程序；真实服务器验证须明确授权目标及范围。

## 可选本机集成

指定已校验来源的 sing-box：

```sh
SBMGR_TEST_SING_BOX=/absolute/path/to/sing-box go test ./cmd/sbmgr -run '^TestMeshSingBox' -count=1 -v
```

覆盖配置校验及 SOCKS5、HY2、WG 单跳/混合线路 TCP/UDP；实际 socket 仅用回环地址，不读取部署数据。测试去除 routing mark，因此不验证 Linux 内核限速。已验证版本为 sing-box 1.14.0，目标机仍须执行自身的配置校验。

Linux 权限测试需 root，使用临时文件与本机 HTTPS：

```sh
CGO_ENABLED=0 go test -c -o /tmp/sbmgr-privilege.test ./cmd/sbmgr
sudo env SBMGR_RUN_PRIVILEGE_TEST=1 /tmp/sbmgr-privilege.test -test.run '^TestSubscriptionPrivilege' -test.v
```

CI 在服务同等 sandbox 下检查 UID/GID、零 capabilities、私有文件拒绝访问和 HTTP 生命周期；另执行 govulncheck、CodeQL 与仓库隐私检查。

## 构建与提交

开发构建：`go build -o sbmgr ./cmd/sbmgr`。正式构建：`./deploy/build-linux.sh`，要求干净工作树且 HEAD 精确对应 `vX.Y.Z` tag，注入版本及 commit；不手写版本历史。部署与来源核验见[运维指南](OPERATIONS.md)。

提交前审阅 `git status --short` 和 `git diff --cached`；不得使用真实部署数据作夹具。测试数据及贡献许可见[贡献指南](../CONTRIBUTING.md)。
