# 开发、发布与部署

## 环境

- Go 1.25 或更新版本
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

## Git 版本模型

软件历史只存在于 Git。正式版本使用 `vX.Y.Z` 注释 tag，例如：

```bash
git status --short
go test ./...
git tag -a v0.22.0 -m "sbmgr v0.22.0"
./deploy/build-linux.sh
```

构建脚本要求 `HEAD` 精确对应 `vX.Y.Z` tag 且工作树完全干净，再把 tag 版本和 commit 写入产物；任一条件不满足都会拒绝正式发布构建。`sbmgr version` 是只读构建信息，不是程序内版本管理器。

可用 `sbmgr version --verbose` 同时核对完整 Git commit。普通 `sbmgr version` 保持单行稳定格式，供外部部署脚本做严格版本检查。

## 状态模式改动

1. 修改结构并递增 `stateVersion`。
2. 在迁移逻辑中为旧数据设置兼容默认值。
3. 增加从旧版本状态读取的测试以及新值的校验测试。
4. 确认保存仍为原子写入、权限 `0600`，并且 CUI 与 daemon 都通过状态锁。
5. 用生产数据的脱敏副本验证时，不得把副本放入仓库或测试夹具。

## 发布检查表

1. 工作树干净，目标 tag 指向已审核 commit。
2. `gofmt`、`go vet ./...`、`go test ./...` 全部通过。
3. 执行 `deploy/build-linux.sh`，记录生成文件的 SHA256。
4. 将候选二进制与对应的外部部署脚本复制到服务器应用目录。
5. 部署脚本先用候选程序检查当前状态、sing-box 配置和限速规则。
6. 部署后检查 `sbmgr version`、`systemctl is-active sbmgr sing-box`、443 入站和 HTTPS 订阅。
7. 部署成功后确认临时旧二进制已经删除；状态与配置备份仍然存在。

软件需要回退时，从 Git 检出目标 tag，重新构建并走同一套外部部署流程。不要把旧二进制长期堆放在服务器，也不要让运行程序替换自身。

通用部署脚本固定读取 `/root/sbmgr/.sbmgr-release.candidate`，并要求显式传入候选文件的 SHA256 或服务器上的校验文件路径。例如先把产物改名上传为该候选文件，再执行：

```bash
/root/sbmgr/deploy/deploy-release.sh <64位SHA256>
```

脚本会把 `state.json`、`config.base.json`、`sing-box.json` 和校验清单持久保存到 `backups/state-config/`，并保留最近 20 组；旧程序只存在于权限为 `0700` 的临时文件，成功或失败恢复后都会删除。

## 敏感信息检查

提交前至少检查：

```bash
git status --short
git diff --cached --stat
git diff --cached
```

不要使用 `git add .` 盲目暂存。应明确加入源码、测试、文档和工程脚本，并确认真实 YAML/JSON、密钥、证书、token、日志、导出、备份与 `.dist/` 未进入索引。
