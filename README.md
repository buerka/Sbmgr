# sbmgr

sbmgr 是面向 Linux 的 sing-box 多用户终端管理器。它在保留运维方基础配置的前提下，管理用户、设备、节点授权、流量策略、订阅交付和配置应用。

项目采用单目录运行模型：程序、SQLite 状态、基础配置、导出、备份和日志均位于可配置的应用目录中。源码版本由 Git 管理，运行程序只负责业务状态和配置恢复。

## 功能概览

- 用户与设备：新增、编辑、启停、删除、批量操作和模板复制。
- 独立身份：每台设备的每个节点使用独立 UUID、`auth_user` 和 routing mark。
- 节点授权：从基础配置中选择可用出口，并为节点设置独立上传、下载限速。
- 流量管理：分别记录上传和下载，支持双向、仅上传或仅下载配额口径。
- 策略控制：月度账期、附加流量包、阶梯限速、到期停用和滑动窗口异常流量保护。
- 来源控制：来源 IP 存档、动态单活、固定绑定、临时换绑和仅告警模式。
- 访问审计：聚合目标域名或 IP、连接、频次和最近访问时间。
- 配置交付：按设备导出 Mihomo YAML，或通过独立 HTTPS URL 和二维码提供订阅。
- 安全应用：生成候选配置，校验 sing-box 与 nftables，失败时恢复上一状态。
- 运维能力：SQLite 一致性备份、审计日志、Webhook 告警、出口健康检查和只读多机汇总。

## 运行要求

生产运行环境：

- Linux 与 systemd
- sing-box
- nftables 与 conntrack
- 支持出站 `routing_mark` 的内核和 sing-box 版本
- 可执行网络与服务管理操作的系统权限

构建需要 Go 1.26.8 或更新版本，推荐使用 `go.mod` 指定的 Go 1.27.1。Windows 可用于开发和单元测试；限速、systemd、证书和部署流程需要在 Linux 上验证。

## 构建

```bash
go test ./...
go build -trimpath -o sbmgr ./cmd/sbmgr
./sbmgr version
```

正式发布构建由 Git tag 驱动：

```bash
./deploy/build-linux.sh
```

发布脚本要求工作树干净，且 `HEAD` 精确对应 `vX.Y.Z` tag。

## 部署

以下占位符表示部署方提供的值：

- `<SBMGR_HOME>`：应用与运行数据目录的绝对路径
- `<SOURCE_CONFIG>`：现有 sing-box JSON 配置
- `<INBOUND_TAG>`：需要管理的 VLESS 入站 tag
- `<PUBLIC_HOST>`：客户端连接使用的域名或 IP
- `<REALITY_PUBLIC_KEY>`：客户端配置使用的 Reality 公钥

首次初始化：

```bash
export SBMGR_HOME=/absolute/path/to/sbmgr

"${SBMGR_HOME}/sbmgr" admin init \
  --config "<SOURCE_CONFIG>" \
  --base "${SBMGR_HOME}/config.base.json" \
  --inbound "<INBOUND_TAG>" \
  --server "<PUBLIC_HOST>" \
  --public-key "<REALITY_PUBLIC_KEY>"
```

默认情况下，现有入站身份会保留为非托管身份。只有显式使用 `--import-users` 时才会将其导入受管用户。

安装 systemd 服务：

```bash
./deploy/install-systemd.sh --home "${SBMGR_HOME}" --component all
systemctl enable --now sbmgr
```

安装脚本根据 `--home`、`SBMGR_HOME` 或脚本位置渲染 unit，不依赖固定安装路径。应用目录及其父目录需要满足脚本执行的所有权和写权限检查。

## 使用

启动管理界面：

```bash
"${SBMGR_HOME}/sbmgr"
```

CUI 分为三个区域：

- 用户：用户、设备、节点、配额、策略、连接、访问记录和导出。
- 线路：客户端入口、sing-box 出站与端点、出口健康状态。
- 运维：告警、订阅、备份、审计、同步、配置应用和多机状态。

日常操作可以从页面菜单发现；快捷键显示在当前页面底部。表单支持方向键、`Home`、`End`、`Backspace` 和 `Delete` 进行就地编辑。非编辑页面会定期刷新后台统计，同时保持当前选择和筛选条件。

完整操作说明见 [用户指南](docs/USER_GUIDE.md)，策略语义见 [策略参考](docs/POLICIES.md)。

## 运行数据

典型应用目录：

```text
<SBMGR_HOME>/
├── sbmgr
├── state.db
├── config.base.json
├── sing-box.json
├── mihomo.template.yaml
├── audit.jsonl
├── exports/
├── backups/
└── logs/
```

`state.db` 是默认业务存储。用户、设备、节点、流量采样、访问目标、来源 IP、连接、账期和告警保存在结构化 SQLite 表中。旧版 `state.json` 仅作为一次性迁移输入；迁移成功后会保留原文件并写入迁移标记。

运行数据可能包含身份、订阅凭据、访问记录或服务器配置，因此不属于源码仓库。仓库忽略常见运行文件和凭据格式，CI 还会检查已跟踪文件中的高风险路径与内容模式。

## 配置与限速模型

sbmgr 不直接修改基础模板中的非托管身份。应用配置时，它从基础模板生成受管用户、路由和专属出站，再用服务器上安装的 sing-box 校验完整候选配置。

实时限速在 sing-box 完成认证和路由选择后执行。每个受管节点映射到独立 routing mark，nftables/conntrack 根据该标记执行上传和下载 policing。此模型允许多个用户共用同一加密入站，同时保持节点级限速隔离。

## 安全与恢复

- 订阅 token 按设备生成，轮换后旧链接失效。
- 公网订阅监听要求 TLS 1.2 或更新版本；请求限流只使用实际连接来源，不信任转发头。同机反向代理后的所有客户端共用回环来源限额，代理层应另行按真实客户端限流。
- 配置应用会先校验候选，保存状态和配置快照，再进行原子替换与服务重载。
- SQLite 备份通过一致性复制与完整性检查生成。
- 管理操作写入脱敏审计日志；密码、私钥、UUID 和订阅 token 不写入操作摘要。
- 软件版本、发布和回退由 Git 与外部部署脚本管理。

安全报告方式见 [SECURITY.md](SECURITY.md)。

## 已知限制

- 节点级实时限速依赖 Linux routing mark、nftables 和 conntrack。
- 访问统计只能观察 sing-box 日志可提供的目标域名或 IP，无法读取 HTTPS 路径、内容或搜索词。
- 出口健康检查验证网络可达性，不等同于上游协议认证成功。
- endpoint 可以查看和安全编辑，但共享接口型 endpoint 不会自动复制成按用户限速的节点。
- 多机页面提供只读汇总，不执行跨服务器批量配置变更。

## 文档

- [用户指南](docs/USER_GUIDE.md)
- [策略参考](docs/POLICIES.md)
- [订阅服务](docs/SUBSCRIPTIONS.md)
- [运维指南](docs/OPERATIONS.md)
- [架构与边界](docs/ARCHITECTURE.md)
- [开发、发布与部署](docs/DEVELOPMENT.md)
- [贡献指南](CONTRIBUTING.md)
- [变更记录](CHANGELOG.md)
