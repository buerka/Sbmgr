# 2026-09-05 安全修复记录

基线 `ba295c1`；对应第二版审计及 GitHub #1–#33。以下是源码修复追踪，不表示已部署或 issue 已关闭。当前规则以[仓库约束](../AGENTS.md)及专题文档为准。

报告编号统一省略 `SBM-` 前缀；同一行的 issue 与报告编号按顺序对应。验证文件均位于 `cmd/sbmgr/`，另标路径除外。

| Issue | 报告编号 | 修复与验证入口 |
| --- | --- | --- |
| #1, #6, #7 | 01, 11, 12 | 日志完整语法与固定身份位置；目标不能伪造来源或关闭事件。 `security_regression_test.go` |
| #2, #4, #11 | 03, 04, 16 | token 索引预检、仅信任实际对端、有界来源/全局预算。 `security_regression_test.go`、`subscription_test.go` |
| #3 | 02 | 限制目标语法/长度，过滤终端控制与 bidi。 `security_regression_test.go` |
| #5, #29 | 05, 32 | mark 固定计数键；耗尽可处理，批量容量预检不部分提交。 `security_policy_test.go`、`security_regression_test.go` |
| #8, #9 | 13, 14 | 保留各入站非托管身份与复杂/混合路由，拒绝身份冲突。 `security_policy_test.go` |
| #10 | 15 | 重载后检查稳定性和入站，失败恢复配置及 nft。 `security_linux_test.go` |
| #12 | 17 | 网络维护锁外执行、锁内条件合并，保留并发编辑。 `security_regression_test.go` |
| #13, #14 | 18, 19 | 访问索引/批量裁剪，连接有界索引堆；含热点基准。 `security_policy_test.go` |
| #15, #16 | 29, 30 | 持久 IP 活动/换绑宽限与自动释放；并发处罚自动恢复，保留手动禁用。 `security_policy_test.go` |
| #17 | 06 | 更新依赖与工具链；加入 govulncheck、CodeQL、Dependabot。版本见 `go.mod`，流程见 `.github/` |
| #18, #24, #27 | 07, 24, 27 | 私有目录/文件权限、realpath 后校验、拒绝配置路径别名/软硬链接。 `security_linux_test.go`、`security_regression_test.go`、`deploy/test-deploy-scripts.sh` |
| #19 | 09 | 专用 UID、全线程降权、有界只读 IPC、父子生命周期。 `subscription_worker_linux_test.go`、`subscription_ipc_test.go` |
| #20, #21, #31 | 20, 21, 08 | 公网通用错误、QR/HEAD/订阅共用可用性校验、TLS ≥1.2。 `security_regression_test.go`、`subscription_tls_test.go` |
| #22, #23 | 22, 23 | 审计白名单、错误脱敏、默认视图隐藏身份与 token、秘密输入。 `security_policy_test.go` |
| #25 | 25 | 续期 sandbox；Certbot 全依赖固定版本/wheel 哈希，禁止源码构建。 `deploy/certbot-requirements.txt`、部署脚本自检 |
| #26 | 26 | Actions 固定 SHA、隐私扫描覆盖 example/二进制、tag 产物来源证明。 `.github/workflows/`、`scripts/test_check_public_tree.py` |
| #28, #30, #33 | 31, 33, 28 | 拒绝非有限/溢出容量、坏封禁时间及危险服务参数；统计 API 限本机。 `security_regression_test.go` |
| #32 | 10 | SSH 引用、超时/WaitDelay、Fleet 固定只读入口。 `fleet_test.go`、`deploy/test-deploy-scripts.sh` |

## 历史验证与升级

当时迁移到业务版本 10 / schema 2；当前版本常量见[代码导航](ARCHITECTURE.md)。旧程序回退需要匹配的部署前快照；新增校验拒绝的旧数据须影子预检修正，详见[运维指南](OPERATIONS.md)。订阅预算与隔离见[订阅服务](SUBSCRIPTIONS.md)，处罚默认值见[策略参考](POLICIES.md)。

当时已执行 Windows/Linux Go 测试、vet、Linux 构建、部署事务/语法自检、隐私扫描及回归、govulncheck；覆盖订阅、迁移、并发编辑和 CUI。Linux 服务命令使用桩；未操作真实服务器、真实 ACME 或目标发行版 sandbox。GitHub CodeQL 与来源证明须由 CI 执行；当前复验命令见[开发验证](DEVELOPMENT.md)。

Certbot 哈希来自对应版本的 PyPI wheel；更新需重新解析完整依赖、核验哈希并测试。产物来源核验和部署验收统一按运维指南执行。
