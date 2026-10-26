## 变更

说明问题、触发条件与修改后的行为。若为缺陷修复，附最小复现或失败测试。

## 兼容与恢复

说明状态/schema 迁移、配置影响、部署影响和失败恢复；不适用项简述原因。

## 验证

列出实际运行的检查及结果，注明未验证范围（例如未在真实 Linux 主机验收）。

- [ ] 已按 AGENTS.md、docs/DEVELOPMENT.md 与 docs/TESTING.md 完成适用验证
- [ ] 新增/修改行为有回归测试；测试只使用合成域名、文档网段和伪凭据
- [ ] `gofmt`、`go vet ./...`、`go test ./...` 通过
- [ ] 涉及并发/事务边界时 `go test -race ./...` 通过
- [ ] 前端变更运行 `npm --prefix frontend test`、`typecheck` 与 `build`
- [ ] 部署/证书变更在 Linux 运行脚本语法检查和 `sh deploy/test-deploy-scripts.sh`
- [ ] 已审阅暂存内容，无数据库、运行配置、证书、日志、备份或真实凭据
