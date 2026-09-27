# 架构与代码入口

## 模型与数据流

用户持有配额和策略；设备持有订阅 token；设备节点持有身份、授权与 routing mark。共享入站认证后按 `auth_user` 路由，nftables/conntrack 按 mark 计量与限速。

用户分组提供可选默认规则，覆盖配额、节点速率、到期日、线路授权和自助设备名额。修改分组时，在同一锁和事务中更新未覆盖该项的成员；运行时仍使用用户与节点的实际字段，配额独立计量。个人编辑与分组规则不同的值在保存边界记录为覆盖项。旧用户迁入空规则的默认分组，原设置不变，见 [用户分组](USER_GROUPS.md)。

普通用户自助设备操作在特权后端验证账号、来源、CSRF、设备归属与并发版本。设备名额默认 0，包含停用设备；自助新增只复制本人已授权设备的线路设置，程序创建新身份。新增和删除由后台在约 1 分钟内自动应用，管理员配置待应用时拒绝自助写入。订阅 token 可独立重置，不改变已导入代理配置的 UUID。

```text
Web API / admin → 跨进程锁 → SQLite 迁移、校验、事务 → state.db
                              ↓
基础模板 + 管理状态 → 候选配置/规则 → 校验、备份 → 应用或回滚
                              ↑
daemon → 计数器与日志 → 用量、账期、策略、待应用状态
Linux 物理网卡采样 → 本机增量统计 → 主机汇总从机采样 → 每台机器的续费周期流量与覆盖率

面板浏览器 → 独立低权限 HTTP → 有界管理 IPC → 来源/登录/角色/CSRF → 白名单业务操作
设备订阅请求 → 低权限 HTTP → 独立有界只读 IPC → 单设备查询
```

网络维护使用“锁内快照 → 锁外探测/投递 → 锁内条件合并”，避免覆盖并发编辑。访问统计只含目标域名/IP、次数与时间；连接数量由日志推断。

## 代码导航

路径均相对仓库根目录；先按符号定位，再读对应实现与专题文档。

| 修改点 | 入口 |
| --- | --- |
| CLI、模型、迁移、配置事务 | `cmd/sbmgr/main.go`：`loadState`、`validateState`、`saveState`、`renderConfig`、`applyState` |
| SQLite、跨进程锁 | `state_sqlite.go`、`state_lock*.go`：`withStateLock` |
| 用户、设备、模板、批量 | `device.go`、`user_template.go`、`batch.go` |
| 自助设备、订阅重置 | `device_self_service.go`、`device_self_service_sqlite.go`、`web_portal_devices.go`；前端 `MyDevices`、`ResetSubscriptionLink` |
| 分组继承、成员事务与持久化 | `user_groups.go`、`group_admin.go`、`group_sqlite.go`；前端 `Groups`、`UserGroupSettings` |
| React / TypeScript / shadcn/ui 页面与状态 | `frontend/src/{pages,components}`、`api.ts`、`store.ts`、`theme.tsx`、`tokens.css` |
| Web 认证、动作、静态资源与降权 | `web_{config,http,account,actions,routes,state,runtime,worker_linux}.go`；`web/dist/` 为不入库的构建产物 |
| 普通用户登录、单次邀请与隔离 | `portal_state.go`、`web_portal.go`、`web_invite.go`；前端 `Portal`、`Activate`、`PortalAccess` |
| 参数化自动化、单文件安装 | `automation.go`、`service.go`、`deploy/embed.go` |
| 后台统计与网络维护 | `daemon.go`、`stats.go`、`usage.go`、`network_maintenance.go`、`machine_traffic*.go` |
| 限速、共享 WG 接入 | `rate.go`、`counter_keys.go`、`wireguard_bridge.go` |
| 账期、配额、处罚 | `billing.go`、`quota.go`、`burst.go`、`policy_recovery.go` |
| 来源、访问、连接 | `ip_policy.go`、`access_policy.go`、`connection_tracking.go` |
| 出站、端点、客户端入口 | `outbound_*.go`、`proxy_admin.go`、`client_endpoint.go` |
| 订阅隔离与生命周期 | `subscription_{backend,http,ipc,worker_linux,supervisor}.go` |
| 主从命令、存储、下发、入口授权 | `mesh_{admin,state,sqlite,routes,coordinator,agent,runtime,access}.go` |
| 拓扑校验与协议编译 | `internal/mesh/{model,transport,config}.go` |
| 备份、审计、巡检、健康 | `backup.go`、`audit.go`、`fleet.go`、`health.go` |

除 `internal/mesh` 外，表中省略目录的文件均在 `cmd/sbmgr/`。当前状态模型为 17、SQLite schema 为 8；版本常量分别在 `main.go:stateVersion` 和 `state_sqlite.go:sqliteSchemaVersion`，两者各自迁移，不按软件版本推断。

## 多机与协议

主机保存用户和拓扑，从机只接收自身执行计划与本机入口的用户授权。成员保存管理标识、SSH 连接和公开客户端入口参数；线路保存入口、路径、逐跳协议及末跳出站。客户端直连获授权的入口，流量不经过额外的管理跳。控制通道为固定命令、有界 JSON RPC；拓扑模块不依赖 SQLite、Web 或 SSH。

入口授权使用有期限的租约；从机累计用量通过持久基线增量汇总到主机。机器流量由 Linux 物理网卡计数器独立采样，主机汇总各从机报告；按管理员手动设置的主机本地日期范围分别统计上传、下载与合计。它包含非代理流量，与用户配额无关。采样覆盖范围单独记录，不能从用户用量反推采样前或中断期间的机器流量。

停用、到期、配额和撤权由主机下发，从机也执行本地配额与租约检查。独立第三方 SOCKS5 封装服务在 sbmgr 之外运行，只以基础出站接入，见 [DataImpulse 封装](DATAIMPULSE_GATEWAY.md)。

协调器持久记录恢复决定，逐机准备，再提交从机与主机，最后确认结束。节点事务可重试；未决事务先恢复，不能保证多机同时切换。用法见[主从管理](MESH.md)。

WG 使用 sing-box 用户态 endpoint。共享 endpoint 前的回环认证桥接为用户提供独立 mark，避免复制 peer 身份；下载规则匹配 conntrack 回复方向，避免回环上传重复计数。

持久文件、迁移与恢复见[运维指南](OPERATIONS.md)；订阅权限和 IPC 契约见[订阅服务](SUBSCRIPTIONS.md)；修改时遵守 [AGENTS.md](../AGENTS.md)。
