# 开发、发布与部署

## 环境

- Go 1.26.8 或更新版本；推荐使用 `go.mod` 指定的 Go 1.27.1
- Linux 运行环境需要 sing-box、nftables、conntrack 和 systemd
- Windows 可以开发和运行大部分单元测试；Linux 集成功能应另外验证

## 本地验证

```bash
gofmt -w cmd/sbmgr
go vet ./...
go test ./...
```

构建当前平台的开发版本：

```bash
go build -o sbmgr ./cmd/sbmgr
./sbmgr version
```

Linux 生产订阅隔离要求 `CGO_ENABLED=0 go build ...`；标准发布脚本已禁用 CGO。工作进程通过 Go 的全线程系统调用降权，不支持该保证的构建会拒绝启动 HTTP。升级隔离版本前重新安装 core unit，以创建 `sbmgr-subscription` 非登录账号并允许启动时切换 UID/GID；无需放宽数据库或应用目录权限。

Linux 权限回归会启动真实低权限子进程和本机回环 HTTPS，只使用临时测试文件。显式启用方式：

```bash
CGO_ENABLED=0 go test -c -o /tmp/sbmgr-privilege.test ./cmd/sbmgr
sudo env SBMGR_RUN_PRIVILEGE_TEST=1 /tmp/sbmgr-privilege.test -test.run '^TestSubscriptionPrivilege' -test.v
```

CI 还在与主服务一致的 systemd sandbox 下执行这些测试，验证 UID/GID、capabilities、私有文件拒绝访问、HTTPS/二维码/撤销和父进程退出。它们不会连接真实服务器或调用生产 sing-box/nftables。新增协议回归覆盖畸形报文、内部预算、超时和通道关闭。

## Git 版本模型

软件历史只存在于 Git。正式版本使用 `vX.Y.Z` 注释 tag：

```bash
git status --short
go test ./...
git tag -a vX.Y.Z -m "sbmgr vX.Y.Z"
./deploy/build-linux.sh
```

构建脚本要求 `HEAD` 精确对应 `vX.Y.Z` tag 且工作树完全干净，再把 tag 版本和 commit 写入产物；任一条件不满足都会拒绝正式发布构建。`sbmgr version` 是只读构建信息，不是程序内版本管理器。

可用 `sbmgr version --verbose` 同时核对完整 Git commit。普通 `sbmgr version` 保持单行稳定格式，供外部部署脚本做严格版本检查。

## 状态模式改动

1. 修改结构并递增 `stateVersion`。
2. 修改表结构时递增独立的 SQLite schema 版本并编写事务迁移；业务模型版本与数据库 schema 版本不能混用。
3. 在迁移逻辑中为旧数据设置兼容默认值，并增加从旧 `state.json` 无损导入、重复迁移幂等、损坏输入不留半成品的测试。
4. 高频统计使用结构化表、稳定主键和增量 UPSERT；后台周期不得清空并重插全部历史。
5. 确认提交使用 SQLite 事务，数据库和 sidecar 权限为 `0600`，CUI 与 daemon 共用状态锁，备份通过 `integrity_check`。
6. 用生产数据的脱敏副本验证时，不得把副本放入仓库或测试夹具。

## 发布检查表

安全审计修复与升级前检查见 [2026-09-05 修复记录](SECURITY-REMEDIATION-20260905.md)。标签 CI 会生成构建来源证明；在可信工作站使用 `gh attestation verify <artifact> -R buerka/Sbmgr` 检查仓库、工作流和预期 tag 后，再把校验和传给部署脚本。SHA256 本身只检验内容一致性。

1. 工作树干净，目标 tag 指向已审核 commit。
2. `gofmt`、`go vet ./...`、`go test ./...` 全部通过。
3. 执行 `deploy/build-linux.sh`，记录生成文件的 SHA256。
4. 将候选二进制与对应的外部部署脚本复制到服务器应用目录。
5. 部署脚本先用候选程序检查当前状态、sing-box 配置和限速规则。
6. 部署后检查 `sbmgr version`、`systemctl is-active sbmgr sing-box`、443 入站和 HTTPS 订阅。
7. 部署成功后确认临时旧二进制已经删除；状态与配置备份仍然存在。

软件回退时，从 Git 检出目标 tag，重新构建并执行同一套外部部署流程。旧二进制只在部署事务中临时存在，运行程序不替换自身。

部署脚本默认从自身所在 `deploy/` 的父目录推导 `<SBMGR_HOME>`；也可以使用 `SBMGR_HOME` 或 `--home` 显式指定安全的绝对目录。候选程序放在 `<SBMGR_HOME>/.sbmgr-release.candidate`，并要求显式传入 SHA256 或服务器上的校验文件路径。例如：

```bash
<SBMGR_HOME>/deploy/install-systemd.sh --home <SBMGR_HOME> --component core
<SBMGR_HOME>/deploy/deploy-release.sh --home <SBMGR_HOME> [--sing-box-bin /absolute/path/to/sing-box] <64位SHA256>
```

脚本会在状态锁内保存 `state.db` 的一致性副本、兼容期旧 `state.json`、迁移标记、`config.base.json`、`sing-box.json` 和校验清单到 `backups/state-config/`，并保留最近 20 组；首次升级会在停服状态下同步完成 JSON → SQLite 影子验证与正式迁移，确认数据库完整后才启动服务。root 服务的应用目录与父目录必须由 root 持有且不可被组/其他用户写入。旧程序只存在于权限为 `0700` 的临时文件，成功或失败恢复后都会删除。

## Fleet 专用密钥

Fleet 巡检应使用独立密钥。目标机器的 `authorized_keys` 用 `restrict` 禁止转发、PTY 与用户 rc，并将命令固定为 `/srv/sbmgr/deploy/fleet-readonly-snapshot.sh --home /srv/sbmgr`（按实际安装目录调整）。配置格式为 `restrict,command="<固定脚本路径> --home <固定安装目录>" <巡检公钥>`；脚本、应用目录和父目录须由 root 所有且不能被其他用户写入。

该脚本忽略 `SSH_ORIGINAL_COMMAND`，只运行 `admin snapshot`，不提供交互 shell。安装权限为 0700，并确保同目录有 `path-lib.sh`。不要把真实 SSH 公钥、私钥或服务器地址提交到仓库。

## 敏感信息检查

提交前运行仓库隐私检查，并审阅暂存内容：

```bash
python3 ./scripts/check_public_tree.py
git status --short
git diff --cached --stat
git diff --cached
```

暂存范围应明确限定为源码、测试、文档和工程脚本。真实 YAML/JSON、密钥、证书、token、日志、导出、备份与构建目录属于部署资料。
