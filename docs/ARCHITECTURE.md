# 架构与边界

## 目标

sbmgr 是部署在单台 Linux 中转机上的 sing-box 多用户 CUI 管理器。它管理用户、设备、UUID、节点授权、流量、配额、限速、订阅、访问/IP 策略以及安全应用配置；它不是包管理器，也不管理自身的软件版本。

## 主要数据流

```text
CUI / 隐藏 admin 维护入口
            │
            ▼
 跨进程锁 → SQLite 事务/迁移/校验 → 原子提交
            │                         │
            │                         ├─ 只读设备查询 → 匿名 IPC → 低权限 HTTP/TLS 进程
            │                         └─ 审计与告警
            ▼
 生成候选 sing-box 配置与 nftables 规则
            │
       check + 快照
            │
            ▼
 sing-box 重载/重启 + nftables/conntrack
            ▲
            │
 daemon：计数器、journal、来源 IP、目标、健康与账期同步
```

## 持久文件

- `state.db`：嵌入式 SQLite 业务数据库。用户、设备、节点、流量采样、用量历史、访问目标、来源 IP、连接、账期和告警分别存入结构化表并建立查询索引；小型策略对象使用受约束的 JSON 列。数据库及 sidecar 权限为 `0600`。
- `state.json`：仅用于从旧版本一次性导入。迁移会在跨进程锁内完成，成功后保留原文件和备份作为人工回退材料，并用 `state.json.migrated` 防止 DB 丢失后静默回灌陈旧统计。
- `config.base.json`：运维方提供的 sing-box 基础模板；受管用户和路由在生成阶段叠加。
- `sing-box.json`：当前生成并应用的运行配置。
- `backups/`：状态、基础模板、运行配置和限速快照；不保存程序版本。
- `audit.jsonl`：人工管理操作的脱敏审计。
- `exports/`：带时间戳的静态 Mihomo 配置；设备订阅则按请求实时生成。

这些都是服务器运行数据，必须留在部署目录并排除出 Git。

## 关键模块

- `cmd/sbmgr/main.go`、`state_sqlite.go`：状态模型、SQLite schema/迁移、命令入口、配置生成与应用事务。
- `cmd/sbmgr/tui.go`：CUI 页面、菜单、表单和刷新。
- `cmd/sbmgr/daemon.go`、`stats.go`、`usage.go`：后台维护、流量同步与实时数据。
- `cmd/sbmgr/rate.go`：按 routing mark 的 nftables 限速与计数。
- `cmd/sbmgr/subscription.go`、`subscription_backend.go`：订阅设置和仅针对单设备的只读查询。
- `subscription_http.go`、`subscription_ipc.go`、`subscription_worker_linux.go`：低权限 HTTP/TLS、受限内部协议与 Linux 降权启动；`subscription_supervisor.go` 负责子进程故障恢复。
- `cmd/sbmgr/ip_policy.go`、`access_policy.go`、`burst.go`：来源 IP、访问/并发和异常流量规则。
- `cmd/sbmgr/outbound_*.go`、`proxy_admin.go`：中转入口、出站和 endpoint 的安全编辑。
- `cmd/sbmgr/backup.go`：使用 SQLite 一致性快照的业务状态备份、完整性验证与恢复。

## 版本职责

Git commit/tag 是软件源码版本的唯一事实来源。构建脚本将 `git describe` 结果写入 `sbmgr version`，仅供识别运行构建。程序不会扫描历史二进制、替换自身或提供软件回滚页面。

外部部署脚本负责安装边界：部署前持久备份业务状态和配置，在部署事务期间保存一个临时旧二进制，失败时恢复，成功后删除。要回退软件，应从 Git 检出目标 tag、重新构建，再运行外部部署流程。

## 安全与隐私边界

- 共享 443 入站先由 sing-box 认证，再通过 `auth_user` 选择带独立 routing mark 的出口；内核不直接解析 UUID。
- 访问统计仅保存目标域名/IP、聚合次数和时间，不解密 HTTPS 内容或 URL 路径。
- 订阅 token 等同访问凭据；公网监听强制 TLS，并限制来源请求速率。
- 完整代理 JSON 可能含凭据，只进入权限受限的临时文件，输出和审计必须脱敏。
- systemd 单元限制权限；root 守护进程管理应用目录。独立 HTTP 进程使用专用非 root UID，无附加组、无 capabilities，不获得数据库或配置目录访问权限。

低权限订阅入口先执行来源/并发预算与 token 格式校验，再通过匿名 socketpair 请求 root 端查询。root 端独立限制总预算和报文大小，只提供单设备只读结果；先用 SQLite 索引确认 token 存在，再读取当前状态生成响应。限流只使用实际连接来源，不信任转发头。exec 后的子进程不继承父进程业务堆、环境或状态文件描述符，接受公网连接前必须完成所有线程的降权。启动与升级要求见 [订阅服务](SUBSCRIPTIONS.md)。

daemon 的网络维护采用“锁内快照 → 锁外探测/投递 → 锁内有条件合并”，避免慢 Webhook 或 Fleet 占据全局状态锁。日志解析只接受完整事件语法；活动连接与最近访问均有容量边界。

状态版本 10 保存 IP 绑定活动时间与并发处罚到期时间；SQLite schema 2 增加订阅 token 查询索引。具体升级行为、限制及审计映射见 [安全审计修复记录](SECURITY-REMEDIATION-20260905.md)。
