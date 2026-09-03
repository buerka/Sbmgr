# 架构与边界

## 目标

sbmgr 是部署在单台 Linux 中转机上的 sing-box 多用户 CUI 管理器。它管理用户、设备、UUID、节点授权、流量、配额、限速、订阅、访问/IP 策略以及安全应用配置；它不是包管理器，也不管理自身的软件版本。

## 主要数据流

```text
CUI / 隐藏 admin 维护入口
            │
            ▼
 跨进程锁 → 读取/迁移/校验 state.json → 原子保存
            │                         │
            │                         ├─ HTTPS 设备订阅 / 用量响应头
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

- `state.json`：业务数据库，包含用户策略和累计统计；原子写入，权限 `0600`。
- `config.base.json`：服务器原有 sing-box 配置的基础模板；受管用户和路由在生成阶段叠加。
- `sing-box.json`：当前生成并应用的运行配置。
- `backups/`：状态、基础模板、运行配置和限速快照；不保存程序版本。
- `audit.jsonl`：人工管理操作的脱敏审计。
- `exports/`：带时间戳的静态 Mihomo 配置；设备订阅则按请求实时生成。

这些都是服务器运行数据，必须留在部署目录并排除出 Git。

## 关键模块

- `cmd/sbmgr/main.go`：状态模型、命令入口、配置生成与应用事务。
- `cmd/sbmgr/tui.go`：CUI 页面、菜单、表单和刷新。
- `cmd/sbmgr/daemon.go`、`stats.go`、`usage.go`：后台维护、流量同步与实时数据。
- `cmd/sbmgr/rate.go`：按 routing mark 的 nftables 限速与计数。
- `cmd/sbmgr/subscription.go`：按设备 token 下发 HTTPS 订阅和用量响应头。
- `cmd/sbmgr/ip_policy.go`、`access_policy.go`、`burst.go`：来源 IP、访问/并发和异常流量规则。
- `cmd/sbmgr/outbound_*.go`、`proxy_admin.go`：中转入口、出站和 endpoint 的安全编辑。
- `cmd/sbmgr/backup.go`：业务状态备份与恢复。

## 版本职责

Git commit/tag 是软件源码版本的唯一事实来源。构建脚本将 `git describe` 结果写入 `sbmgr version`，仅供识别运行构建。程序不会扫描历史二进制、替换自身或提供软件回滚页面。

外部部署脚本负责安装边界：部署前持久备份业务状态和配置，在部署事务期间保存一个临时旧二进制，失败时恢复，成功后删除。要回退软件，应从 Git 检出目标 tag、重新构建，再运行外部部署流程。

## 安全与隐私边界

- 共享 443 入站先由 sing-box 认证，再通过 `auth_user` 选择带独立 routing mark 的出口；内核不直接解析 UUID。
- 访问统计仅保存目标域名/IP、聚合次数和时间，不解密 HTTPS 内容或 URL 路径。
- 订阅 token 等同访问凭据；公网监听强制 TLS，并限制来源请求速率。
- 完整代理 JSON 可能含凭据，只进入权限受限的临时文件，输出和审计必须脱敏。
- systemd 单元限制权限；只有应用目录可写。
