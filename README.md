# sbmgr

Linux 上的 sing-box 多用户管理器，使用 Go 编写，提供编译进单个二进制的中文 Web 管理面板。管理用户、设备、节点授权、配额、限速、访问策略和 Mihomo 订阅，支持多机线路与中转。

公开仓库只包含通用源码、构建文件和使用文档；生产状态、真实用户信息、代理配置、凭据、证书、日志、备份和部署验收资料均属于私有运行数据。

## 使用

运行环境：Linux、systemd、sing-box、nftables、conntrack，以及网络和服务管理权限。受管客户端入口使用 VLESS + REALITY；首次初始化需准备可用的 sing-box 基础配置。已有身份默认保留，明确导入后才受管理。

Go 版本以 [go.mod](go.mod) 为准；前端构建需要 Node.js 22.12+（CI 使用 24）。Windows 可用于本地构建检查。开发构建：

```sh
npm --prefix frontend ci
npm --prefix frontend run build
go test ./...                         # 编译所有 Go 包
go build -trimpath -o sbmgr ./cmd/sbmgr
./sbmgr version
```

按[运维指南](docs/OPERATIONS.md)初始化 sing-box 基础配置后：

```sh
./sbmgr web configure --password-file /root/sbmgr-admin.secret
./sbmgr service install
./sbmgr service start
```

默认监听 `127.0.0.1:9090`，通过 SSH 本地转发访问；公网 HTTPS 和反向代理配置见 [Web 管理](docs/WEB_ADMIN.md)。页面、API 与部署工具都在 `sbmgr` 内，运行时无需 Node.js、前端目录或 CDN。直接运行 `sbmgr` / `sbmgr serve` 启动前台服务；`admin` 参数命令用于自动化。原 TUI、交互菜单已移除。

## 文档入口

| 任务 | 文档 |
| --- | --- |
| 接手代码 | [仓库约束](AGENTS.md) → [架构与代码入口](docs/ARCHITECTURE.md) → [开发验证](docs/DEVELOPMENT.md) |
| 日常操作 | [用户指南](docs/USER_GUIDE.md) · [策略参考](docs/POLICIES.md) |
| 订阅交付与服务 | [订阅服务](docs/SUBSCRIPTIONS.md) |
| 节点接入、线路编排 | [主从管理](docs/MESH.md) |
| 初始化、部署、备份、巡检 | [运维指南](docs/OPERATIONS.md) |
| 贡献与历史 | [贡献指南](CONTRIBUTING.md) · [变更记录](CHANGELOG.md) |
| 安全报告 | [安全策略](SECURITY.md) |

`state.db`、配置、凭据和备份属于部署资料，不进入源码仓库。软件版本由 Git tag 和外部部署流程管理。

许可证：[GPL-3.0-only](LICENSE)。第三方内容保留各自版权、许可证及 NOTICE 要求。
