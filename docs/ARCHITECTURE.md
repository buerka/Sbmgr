# 架构与代码入口

## 模型与数据流

用户持有配额和策略；设备持有订阅 token；设备节点持有身份、授权与 routing mark。共享入站认证后按 `auth_user` 路由，nftables/conntrack 按 mark 计量与限速。

```text
CUI / admin → 跨进程锁 → SQLite 迁移、校验、事务 → state.db
                              ↓
基础模板 + 管理状态 → 候选配置/规则 → 校验、备份 → 应用或回滚
                              ↑
daemon → 计数器与日志 → 用量、账期、策略、待应用状态

设备订阅请求 → 低权限 HTTP → 有界只读 IPC → 单设备查询
```

网络维护使用“锁内快照 → 锁外探测/投递 → 锁内条件合并”，避免覆盖并发编辑。访问统计只含目标域名/IP、次数与时间；连接数量由日志推断。

## 代码导航

路径均相对仓库根目录；先按符号定位，再读相关测试。

| 修改点 | 入口 |
| --- | --- |
| CLI、模型、迁移、配置事务 | `cmd/sbmgr/main.go`：`loadState`、`validateState`、`saveState`、`renderConfig`、`applyState` |
| SQLite、跨进程锁 | `state_sqlite.go`、`state_lock*.go`：`withStateLock` |
| 用户、设备、模板、批量 | `device.go`、`user_template.go`、`batch.go` |
| CUI、交付菜单 | `tui.go`、`tui_mesh.go`、`tui_subscription_delivery.go` |
| 后台统计与网络维护 | `daemon.go`、`stats.go`、`usage.go`、`network_maintenance.go` |
| 限速、共享 WG 接入 | `rate.go`、`counter_keys.go`、`wireguard_bridge.go` |
| 账期、配额、处罚 | `billing.go`、`quota.go`、`burst.go`、`policy_recovery.go` |
| 来源、访问、连接 | `ip_policy.go`、`access_policy.go`、`connection_tracking.go` |
| 出站、端点、客户端入口 | `outbound_*.go`、`proxy_admin.go`、`client_endpoint.go` |
| 订阅隔离与生命周期 | `subscription_{backend,http,ipc,worker_linux,supervisor}.go` |
| 主从命令、存储、下发 | `mesh_{admin,state,sqlite,routes,coordinator,agent,runtime}.go` |
| 拓扑校验与协议编译 | `internal/mesh/{model,transport,config}.go` |
| 备份、审计、巡检、健康 | `backup.go`、`audit.go`、`fleet.go`、`health.go` |

除 `internal/mesh` 外，表中省略目录的文件均在 `cmd/sbmgr/`。业务版本取 `main.go:stateVersion`，数据库版本取 `state_sqlite.go:sqliteSchemaVersion`；两者各自迁移，不按软件版本推断。

## 多机与协议

主机保存用户和拓扑，从机只接收自身执行计划。成员保存管理标识和 SSH 连接；线路保存路径与逐跳协议，末跳直接出站。控制通道为固定命令、有界 JSON RPC；拓扑模块不依赖 SQLite、CUI 或 SSH。

协调器持久记录恢复决定，逐机准备，再提交从机与主机，最后确认结束。节点事务可重试；未决事务先恢复，不能保证多机同时切换。用法见[主从管理](MESH.md)。

WG 使用 sing-box 用户态 endpoint。共享 endpoint 前的回环认证桥接为用户提供独立 mark，避免复制 peer 身份；下载规则匹配 conntrack 回复方向，避免回环上传重复计数。

持久文件、迁移与恢复见[运维指南](OPERATIONS.md)；订阅权限和 IPC 契约见[订阅服务](SUBSCRIPTIONS.md)；修改时遵守 [AGENTS.md](../AGENTS.md)。
