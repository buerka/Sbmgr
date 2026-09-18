# 主从管理与线路

主机管理用户、设备、配额和订阅，通过 SSH 下发从机执行计划。每条线路可选择独立客户端入口、有序中转节点和末跳出站；从机入口直接处理流量，不经主机绕行。省略入口沿用主机，省略末跳出站直接落地。路径只含入口自身时在入口落地。

从机入口需要先准备自己的 VLESS + REALITY 基础配置，再在主机的“线路与服务器 → 入口设置”表单填写公开参数（`server`、`port`、`server_name`、`reality_public_key`、`short_id`）。私钥留在从机。应用拓扑后，设备分配线路会同时决定入口和落地；只有获授权的线路才进入该设备订阅。已有用户不自动获得新线路。

```sh
sbmgr admin mesh entry --id relay-a --file '<ENTRY_PUBLIC_JSON>'
sbmgr admin mesh route --id relay-local --entry relay-a --hops relay-a
sbmgr admin mesh route --id relay-external --entry relay-a --hops relay-a --exit external-socks
sbmgr admin mesh route --id master-external --hops relay-a --exit external-socks
```

`--exit` 引用最后一台机器基础配置的出站 tag，未找到时拒绝应用；外部落地凭据只需配置在该机器。管理协议版本为 2，多机升级后再应用新拓扑。

主机后台默认约每 5 秒汇总从机累计用量并续发授权；用量基线持久保存在结构化计数表，重试不会重复计费。从机保留独立计量，权限、停用和到期由主机同步；90 秒授权租约失效后，在下一次后台检查关闭受管入口身份，已有转发连接随配置重启断开。主从服务均须持续运行。链路中断、采样及重载存在延迟，跨机配额不是同时生效的全局硬截止；从机只获得当期剩余配额的一部分。动态来源学习和连接计数在各入口执行。

可通过“同步入口授权”或 `sbmgr admin mesh sync` 手工立即同步。同步不改变用户既有节点授权。受管身份只能在其指定入口认证；节点之间的中转监听使用单独的传输凭据。

```text
客户端 → 主机入口 ── SOCKS5 → 从机 A ── WG → 从机 B → 目标
客户端 → 主机入口 ── HY2 → 从机 A → 目标
```

## 协议

| 值（别名） | 行为 |
| --- | --- |
| `socks`（`socks5`、`s5`） | 自动认证；TCP/UDP，节点间 UDP 使用 UoT；本身不加密 |
| `hysteria2`（`hy2`、`hy`） | 新线路默认；UDP 监听，自动生成并验证独立 TLS 证书，有效期一年 |
| `wireguard`（`wg`） | UDP 监听，逐连接密钥与用户态 endpoint；需 sing-box WireGuard/gVisor 支持 |

所有机器需兼容的 sbmgr、sing-box；HY2 需 QUIC 支持。主机 SSH 可达从机，相邻节点的数据端口互通，管理地址与数据地址可分别设置。目标机的 `sing-box check` 是应用前门禁。

已有 WG 服务可在“线路与服务器 → 添加出站 → 端点”导入 `wireguard` JSON，使用 `system: false` 并填写远端、地址和 peer 参数，再分配给设备、应用配置。共享端点经回环认证桥接保留独立计量/限速；内部端口从 48000 起避开配置中的监听，冲突按配置事务回滚。

## 接入与编排

主机先按[运维指南](OPERATIONS.md)初始化。Web“线路与服务器”提供登记、入口设置、编排、同步和应用；接入文件的导出与加入保留为 CLI 自动化命令；示例路径须替换为实际绝对路径。

主机登记并导出接入文件（0600，目标文件须不存在）：

```sh
sbmgr admin mesh init --id master --cluster default
sbmgr admin mesh add --id relay-a --host '<SSH_HOST>' --port 22 \
  --user root --key '<PRIVATE_SSH_KEY_PATH>' --home /srv/sbmgr
sbmgr admin mesh export --node relay-a --output '<ABSOLUTE_JOIN_FILE>'
```

从机加入并安装服务。全新目录自动生成最小回环基础配置和数据库，拒绝覆盖已有配置或身份：

```sh
export SBMGR_HOME=/srv/sbmgr
"$SBMGR_HOME/sbmgr" admin mesh join --file '<ABSOLUTE_JOIN_FILE>'
"$SBMGR_HOME/deploy/install-systemd.sh" --home "$SBMGR_HOME" --component core
```

管理公钥在从机 `authorized_keys` 中限制为：

```text
restrict,command="/srv/sbmgr/deploy/mesh-agent-rpc.sh --home /srv/sbmgr" <PUBLIC_KEY>
```

脚本权限 0700；程序、脚本、应用目录及父目录由 root 持有且不可被其他用户修改。提前核验主机指纹；客户端严格检查 known_hosts，不接受交互认证。固定入口忽略 `SSH_ORIGINAL_COMMAND`，只接受状态、prepare/commit/rollback/finalize 和 usage/access；消息 ≤2 MiB，校验协议版本、集合、身份和角色，不接收任意命令或完整配置。

主机编排并应用：

```sh
sbmgr admin mesh route --id near --hops relay-a --protocols hy2
sbmgr admin mesh route --id home --hops master
# 先登记 exit-b，再创建混合线路
sbmgr admin mesh route --id chain --hops relay-a,exit-b \
  --protocols socks,wg --endpoints '<RELAY_HOST>:21000,<EXIT_HOST>:22000'
sbmgr admin mesh check
sbmgr admin mesh apply
```

- 同标识修改线路；协议可填一个统一值或逐跳列表。省略保留原值，新连接默认 HY2；地址省略保留，新连接使用 SSH 主机名及从 20000 起的可用端口。
- 协议改变或 `route ... --rotate` 更新认证材料；HY2 证书过期可编辑/轮换，但拒绝应用。最多 128 节点、256 线路、每条 16 跳，禁止循环。
- 应用成功后，设备“分配节点”选择 `主从 · <线路标识>`，设置速率，再“应用配置”；从机执行 `systemctl enable --now sbmgr` 开启维护。
- 删除用 `remove-route --id <ROUTE_ID>` 或 `remove --id <MEMBER_ID>`。先迁移用户、消除引用并成功应用，使旧监听停用；删除登记不清理远端数据或 SSH 授权，闲置管理密钥需撤销。

## 事务与验收

编辑保存待应用修订。协调器持久记录恢复决定，逐机准备、校验并备份，先提交从机再切换主机，全部成功后确认结束。只下发本机所需计划和密钥；多机不保证同时切换，重启可能断开连接。

中断后未决事务阻止新拓扑变更；恢复通信后运行 `sbmgr admin mesh recover`。已有最终提交决定则继续确认，否则恢复旧计划；同一事务可幂等重试，业务备份保留。

`mesh check` 只验管理通道和修订。实际验收须覆盖线路 TCP/UDP、出口、订阅、停用、配额及双向限速；目标环境验收在部署方私有流程中执行。只读 Fleet 巡检设置见[运维指南](OPERATIONS.md)。
