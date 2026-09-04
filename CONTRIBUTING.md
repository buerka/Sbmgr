# 贡献指南

## 开发流程

每个提交应保持单一目的，并包含相应测试和文档。仓库约束见 [AGENTS.md](AGENTS.md)，完整验证与发布流程见 [docs/DEVELOPMENT.md](docs/DEVELOPMENT.md)。

提交前运行：

```bash
gofmt -w ./cmd/sbmgr
go vet ./...
go test ./...
python3 ./scripts/check_public_tree.py
```

拉取请求应说明：

- 用户可见变化；
- 状态与配置兼容性；
- 失败和恢复行为；
- 已执行的验证；
- 涉及的安全或隐私边界。

## 测试数据

测试夹具使用下列保留资源：

- IPv4：RFC 5737 的 `192.0.2.0/24`、`198.51.100.0/24`、`203.0.113.0/24`
- 域名：`.example` 或 `.invalid`
- 身份：`alice`、`user-a`、`node-a` 等显式虚构值
- 凭据：具有 `fixture-`、`test-` 等前缀的不可用值

真实服务器地址、UUID、代理凭据、订阅 token、私钥、证书和访问记录属于部署数据，不用于 issue、提交、测试夹具或截图。

## 兼容性

持久状态结构变化需要版本迁移、旧状态测试和幂等验证。配置生成或应用流程变化需要覆盖校验、失败恢复和非托管配置保留。Linux 专属行为应在 Linux 环境完成集成验证。
