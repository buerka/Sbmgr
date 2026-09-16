# sbmgr

Linux 上的 sing-box 多用户管理器，使用 Go 编写，提供中文终端界面。管理用户、设备、节点授权、配额、限速、访问策略和 Mihomo 订阅，支持多机线路与中转。

## 使用

运行环境：Linux、systemd、sing-box、nftables、conntrack，以及网络和服务管理权限。受管客户端入口使用 VLESS + REALITY；首次初始化需准备可用的 sing-box 基础配置。已有身份默认保留，明确导入后才受管理。

Go 版本以 [go.mod](go.mod) 为准；Windows 可开发和运行单元测试。开发构建：

```sh
go test ./...
go build -trimpath -o sbmgr ./cmd/sbmgr
./sbmgr version
```

按[运维指南](docs/OPERATIONS.md)初始化并安装服务，在服务器或 SSH 终端运行 `sbmgr` 进入管理界面。用户与设备在“用户”页管理，出站在“线路”页管理，订阅、备份、配置应用和主从管理在“运维”页。

## 文档入口

| 任务 | 文档 |
| --- | --- |
| 接手代码 | [仓库约束](AGENTS.md) → [架构与代码入口](docs/ARCHITECTURE.md) → [开发验证](docs/DEVELOPMENT.md) |
| 日常操作 | [用户指南](docs/USER_GUIDE.md) · [策略参考](docs/POLICIES.md) |
| 订阅交付与服务 | [订阅服务](docs/SUBSCRIPTIONS.md) |
| 节点接入、线路编排 | [主从管理](docs/MESH.md) |
| 初始化、部署、备份、巡检 | [运维指南](docs/OPERATIONS.md) |
| 贡献与历史 | [贡献指南](CONTRIBUTING.md) · [变更记录](CHANGELOG.md) |
| 安全报告与修复追踪 | [安全策略](SECURITY.md) · [审计记录](docs/SECURITY-REMEDIATION-20260905.md) |

`state.db`、配置、凭据和备份属于部署资料，不进入源码仓库。软件版本由 Git tag 和外部部署流程管理。

许可证：[GPL-3.0-only](LICENSE)。第三方内容保留各自版权、许可证及 NOTICE 要求。
